package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/httpx"
	"github.com/windoze95/cantinarr-server/internal/httpx/httpxtest"
)

// The whole point of the opt-in, proved on the path that actually carries a
// turn rather than on the transport in isolation: an endpoint the admin has
// declared internet-bound leaves through the outbound proxy, and one they
// have not is dialed directly even while a proxy is installed.
func TestLocalOpenAITurnRidesTheProxyOnlyWhenOptedIn(t *testing.T) {
	t.Cleanup(func() { httpx.SetOutboundProxy(nil) })
	upstream := make(chan providerRequest, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream <- captureProviderRequest(r)
		writeOpenAITextSSE(w)
	}))
	t.Cleanup(server.Close)
	proxy := httpxtest.New(t)
	httpx.SetOutboundProxy(proxy.URL())

	h, _, _, _ := newResolverTestHandler(t)
	h.validationProbe = nil
	profile := credentials.AIProfile{
		Config:            credentials.AIConfig{Provider: credentials.AIProviderLocalOpenAI, Model: "qwen3.6:35b-a3b"},
		CredentialPresent: true,
		BaseURL:           server.URL + "/v1",
	}

	// Opted out: the LAN assumption every install had before this setting.
	if err := h.ValidateSharedAISettings(context.Background(), profile); err != nil {
		t.Fatalf("validate direct local profile: %v", err)
	}
	if got := (<-upstream).path; got != "/v1/chat/completions" {
		t.Fatalf("direct turn path=%q", got)
	}
	if hits := proxy.Hits(); len(hits) != 0 {
		t.Fatalf("a direct local turn went through the proxy: %#v", hits)
	}

	// Opted in: the same endpoint, now declared an internet host. The fake
	// proxy answers JSON rather than an SSE stream, so the turn itself fails;
	// the hit is the assertion, and it is the thing that was leaking.
	profile.UseProxy = true
	_ = h.ValidateSharedAISettings(context.Background(), profile)
	hits := proxy.Hits()
	if len(hits) == 0 {
		t.Fatal("an opted-in local turn never reached the proxy")
	}
	target, err := url.Parse(hits[0].Target)
	if err != nil {
		t.Fatalf("proxy saw a non-absolute target %q: %v", hits[0].Target, err)
	}
	if target.Host != strings.TrimPrefix(server.URL, "http://") || target.Path != "/v1/chat/completions" {
		t.Fatalf("proxy target = %q, want the configured endpoint", hits[0].Target)
	}
	select {
	case extra := <-upstream:
		t.Fatalf("the opted-in turn reached the endpoint directly as well: %#v", extra)
	default:
	}
}
