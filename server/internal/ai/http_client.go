package ai

import (
	"net/http"
	"time"

	"github.com/windoze95/cantinarr-server/internal/httpx"
)

const providerResponseHeaderTimeout = 10 * time.Minute

// newHostedProviderHTTPClient builds the client for every hosted AI provider
// (Anthropic, OpenAI, Gemini, xAI): internet-bound, so it rides the admin's
// outbound proxy or, failing that, the environment's. Provider keys live in
// custom headers as well as Authorization, so redirects are never followed:
// even a same-domain redirect must not replay a credential-bearing request to
// a different endpoint.
//
// The transport is cloned per construction and therefore pins the proxy as of
// that moment. That is fine only because every provider service is built per
// turn (handler, shared_turn, validation) and never cached; a cached service
// would keep talking to a proxy the admin has since changed.
func newHostedProviderHTTPClient(timeout time.Duration) *http.Client {
	return newProviderHTTPClient(timeout, httpx.ExternalTransport(), httpx.External())
}

// newLocalProviderHTTPClient builds the client for the Local
// (OpenAI-compatible) provider, whose endpoint is admin-typed and usually a
// LAN host: cluster-internal, never proxied.
func newLocalProviderHTTPClient(timeout time.Duration) *http.Client {
	return newProviderHTTPClient(timeout, httpx.Internal(), nil)
}

func newProviderHTTPClient(timeout time.Duration, base *http.Transport, fallback http.RoundTripper) *http.Client {
	return &http.Client{
		Transport:     providerTransport(base, fallback),
		Timeout:       timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// providerTransport clones base with the long response-header budget a
// streaming provider needs; fallback covers the one case base is nil (a test
// has replaced http.DefaultTransport with a non-Transport).
func providerTransport(base *http.Transport, fallback http.RoundTripper) http.RoundTripper {
	if base == nil {
		return fallback
	}
	cloned := base.Clone()
	cloned.ResponseHeaderTimeout = providerResponseHeaderTimeout
	return cloned
}
