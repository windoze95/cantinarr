package ai

import (
	"net/http"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/httpx"
	"github.com/windoze95/cantinarr-server/internal/httpx/httpxtest"
)

// TestProviderClientsPickTheirTransportClass: hosted providers ride the
// admin's outbound proxy; the Local provider does so only when the admin has
// declared its endpoint an internet host.
func TestProviderClientsPickTheirTransportClass(t *testing.T) {
	t.Cleanup(func() { httpx.SetOutboundProxy(nil) })
	proxy := httpxtest.New(t)
	httpx.SetOutboundProxy(proxy.URL())

	hosted := newHostedProviderHTTPClient(time.Second)
	hostedTransport, ok := hosted.Transport.(*http.Transport)
	if !ok || hostedTransport.Proxy == nil {
		t.Fatalf("hosted client transport = %T with nil Proxy; want a proxied clone", hosted.Transport)
	}
	if hostedTransport.ResponseHeaderTimeout != providerResponseHeaderTimeout {
		t.Errorf("hosted ResponseHeaderTimeout = %v, want %v", hostedTransport.ResponseHeaderTimeout, providerResponseHeaderTimeout)
	}
	req, _ := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions", nil)
	if got, err := hostedTransport.Proxy(req); err != nil || got == nil || got.String() != proxy.Server.URL {
		t.Errorf("hosted Proxy(openai) = %v, %v; want %s", got, err, proxy.Server.URL)
	}

	local := newLocalProviderHTTPClient(time.Second, false)
	localTransport, ok := local.Transport.(*http.Transport)
	if !ok || localTransport.Proxy != nil {
		t.Fatalf("local client transport = %T (proxied: %t); want a direct clone", local.Transport, ok && localTransport.Proxy != nil)
	}
	if localTransport.ResponseHeaderTimeout != providerResponseHeaderTimeout {
		t.Errorf("local ResponseHeaderTimeout = %v, want %v", localTransport.ResponseHeaderTimeout, providerResponseHeaderTimeout)
	}
	if localTransport == httpx.Internal() {
		t.Error("the local client must clone Internal() rather than mutate the shared transport")
	}

	// The same provider with the endpoint declared internet-bound.
	proxied := newLocalProviderHTTPClient(time.Second, true)
	proxiedTransport, ok := proxied.Transport.(*http.Transport)
	if !ok || proxiedTransport.Proxy == nil {
		t.Fatalf("opted-in local transport = %T with nil Proxy; want a proxied clone", proxied.Transport)
	}
	if proxiedTransport.ResponseHeaderTimeout != providerResponseHeaderTimeout {
		t.Errorf("opted-in local ResponseHeaderTimeout = %v, want %v", proxiedTransport.ResponseHeaderTimeout, providerResponseHeaderTimeout)
	}
	localReq, _ := http.NewRequest(http.MethodPost, "http://llm-host:11434/v1/chat/completions", nil)
	if got, err := proxiedTransport.Proxy(localReq); err != nil || got == nil || got.String() != proxy.Server.URL {
		t.Errorf("opted-in local Proxy(endpoint) = %v, %v; want %s", got, err, proxy.Server.URL)
	}
	if proxiedTransport == httpx.Internal() {
		t.Error("the opted-in client must not reach for the cluster-internal transport")
	}
}

// TestLocalProviderOptInFollowsAProxyChange: the class is resolved when the
// client is built, and every local service is built per turn, so an admin who
// changes the proxy is followed by the next turn without a restart.
func TestLocalProviderOptInFollowsAProxyChange(t *testing.T) {
	t.Cleanup(func() { httpx.SetOutboundProxy(nil) })
	first := httpxtest.New(t)
	httpx.SetOutboundProxy(first.URL())

	req, _ := http.NewRequest(http.MethodPost, "http://llm-host:11434/v1/chat/completions", nil)
	before := newLocalProviderHTTPClient(time.Second, true).Transport.(*http.Transport)
	if got, _ := before.Proxy(req); got == nil || got.String() != first.Server.URL {
		t.Fatalf("first client Proxy = %v, want %s", got, first.Server.URL)
	}

	second := httpxtest.New(t)
	httpx.SetOutboundProxy(second.URL())
	after := newLocalProviderHTTPClient(time.Second, true).Transport.(*http.Transport)
	if got, _ := after.Proxy(req); got == nil || got.String() != second.Server.URL {
		t.Errorf("second client Proxy = %v, want %s", got, second.Server.URL)
	}
}

// TestHostedProviderClientWithoutDefaultTransport: when a test harness has
// replaced http.DefaultTransport with something else, the hosted client falls
// back to the External round tripper instead of panicking or going nil.
func TestHostedProviderClientWithoutDefaultTransport(t *testing.T) {
	t.Cleanup(func() { httpx.SetOutboundProxy(nil) })
	original := http.DefaultTransport
	http.DefaultTransport = http.RoundTripper(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: r}, nil
	}))
	t.Cleanup(func() { http.DefaultTransport = original })

	client := newHostedProviderHTTPClient(time.Second)
	if client.Transport != httpx.External() {
		t.Fatalf("transport = %T, want httpx.External()", client.Transport)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
