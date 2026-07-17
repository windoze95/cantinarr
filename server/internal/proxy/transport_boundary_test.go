package proxy

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"strings"
	"sync/atomic"
	"testing"
)

func upstreamControlHeaderNames() []string {
	return []string{
		"Cloudflare-CDN-Cache-Control",
		"X-Accel-Buffering",
		"X-Accel-Charset",
		"X-Accel-Expires",
		"X-Accel-Limit-Rate",
		"X-Accel-Redirect",
		"X-Accel-Synthetic-Control",
		"X-Lighttpd-Send-File",
		"X-Lighttpd-Send-Private-File",
		"X-Lighttpd-Send-Tempfile",
		"X-Reproxy-URL",
		"X-Sendfile",
		"X-Sendfile-Temporary",
		"X-Sendfile2",
	}
}

func addUpstreamControlHeaders(header http.Header) {
	for _, name := range upstreamControlHeaderNames() {
		header.Set(name, "public-proxy-control-secret")
	}
}

func upstreamSharedCacheHeaderNames() []string {
	return []string{
		"Age",
		"Akamai-Cache-Control",
		"Cache-Control",
		"CDN-Cache-Control",
		"Cloudflare-CDN-Cache-Control",
		"Edge-Control",
		"Expires",
		"Netlify-CDN-Cache-Control",
		"Pragma",
		"Surrogate-Control",
		"Vercel-CDN-Cache-Control",
		"X-Cache-Control",
		"X-Edge-Control",
	}
}

func addUpstreamSharedCacheHeaders(header http.Header) {
	for _, name := range upstreamSharedCacheHeaderNames() {
		header.Set(name, "public, max-age=86400")
	}
}

func assertPrivateProxyCachePolicy(t *testing.T, header http.Header) {
	t.Helper()
	if got := header.Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("Cache-Control=%q, want private, no-store", got)
	}
	if got := header.Get("Pragma"); got != "no-cache" {
		t.Errorf("Pragma=%q, want no-cache", got)
	}
	for _, name := range []string{"Age", "Akamai-Cache-Control", "CDN-Cache-Control", "Cloudflare-CDN-Cache-Control", "Edge-Control", "Expires", "Netlify-CDN-Cache-Control", "Surrogate-Control", "Vercel-CDN-Cache-Control", "X-Cache-Control", "X-Edge-Control"} {
		if values := header.Values(name); len(values) != 0 {
			t.Errorf("response retained shared-cache header %s=%q", name, values)
		}
	}
}

func assertNoSharedCacheHeaders(t *testing.T, header http.Header) {
	t.Helper()
	for _, name := range upstreamSharedCacheHeaderNames() {
		if values := header.Values(name); len(values) != 0 {
			t.Errorf("informational response retained cache header %s=%q", name, values)
		}
	}
}

func assertNoUpstreamControlHeaders(t *testing.T, header http.Header) {
	t.Helper()
	for _, name := range upstreamControlHeaderNames() {
		if values := header.Values(name); len(values) != 0 {
			t.Errorf("response retained upstream control header %s=%q", name, values)
		}
	}
}

// SEC-004: client identity, routing overrides, and trailers terminate before the upstream request.
func TestInstanceProxyDropsClientRoutingMetadataAndRequestTrailers(t *testing.T) {
	const trailerSecret = "synthetic-request-trailer-secret"
	proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		for _, name := range []string{
			"Cf-Access-Jwt-Assertion",
			"Dpop",
			"Origin",
			"Remote-Email",
			"Remote-Name",
			"Remote-User",
			"Referer",
			"Ssl-Client-Cert",
			"Trailer",
			"X-Authenticated-User",
			"X-Authentik-Email",
			"X-Client-Cert",
			"X-Email",
			"X-Envoy-Original-Path",
			"X-Envoy-Peer-Metadata",
			"X-Groups",
			"X-Identity-Subject",
			"X-Keycloak-User",
			"X-Ms-Client-Principal",
			"X-Pomerium-Claim-Email",
			"X-Roles",
			"X-Spiffe-Id",
			"X-Ssl-Client-Cert",
			"X-Tls-Client-Cert",
			"X-User",
			"X-User-Email",
			"X-Amzn-Oidc-Data",
			"X-Amzn-Oidc-Identity",
			"X-Auth-Request-User",
			"X-Goog-Authenticated-User-Email",
			"X-Goog-Iap-Jwt-Assertion",
			"X-Http-Method",
			"X-Http-Method-Override",
			"X-Method-Override",
			"X-Original-Url",
			"X-Rewrite-Url",
		} {
			if values := r.Header.Values(name); len(values) != 0 {
				t.Errorf("upstream received client routing header %s=%q", name, values)
			}
		}
		if len(r.Trailer) != 0 {
			t.Errorf("upstream received client trailers: %#v", r.Trailer)
		}
		if got := r.Header.Get("X-Api-Key"); got != "test-instance-api-key" {
			t.Errorf("upstream X-Api-Key=%q, want configured instance key", got)
		}
		if got := r.Header.Get("Accept-Encoding"); got != "identity" {
			t.Errorf("upstream Accept-Encoding=%q, want identity", got)
		}
		if got := r.Header.Get("X-Request-Id"); got != "safe-request-id" {
			t.Errorf("upstream X-Request-Id=%q, want safe-request-id", got)
		}
		if got := r.URL.Query().Get("page"); got != "2" {
			t.Errorf("upstream safe query page=%q, want 2", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))

	req, err := http.NewRequest(http.MethodPost, proxyURL+"/api/instances/"+instanceID+"/api/v3/movie?page=2", io.NopCloser(strings.NewReader("synthetic body")))
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = -1
	req.Header.Set("Origin", "https://hostile.invalid")
	req.Header.Set("CF-Access-JWT-Assertion", "synthetic-cloudflare-identity-jwt")
	req.Header.Set("DPoP", "synthetic-proof-jwt")
	req.Header.Set("Remote-Email", "private@example.invalid")
	req.Header.Set("Remote-Name", "Synthetic Private Name")
	req.Header.Set("Remote-User", "synthetic-private-identity")
	req.Header.Set("Referer", "https://cantinarr.invalid/page?token=referer-secret")
	req.Header.Set("SSL-Client-Cert", "synthetic-client-certificate")
	req.Header.Set("X-Amzn-Oidc-Data", "synthetic-aws-identity-jwt")
	req.Header.Set("X-Amzn-Oidc-Identity", "synthetic-aws-identity")
	req.Header.Set("X-Auth-Request-User", "synthetic-oauth-proxy-identity")
	req.Header.Set("X-Authenticated-User", "synthetic-authenticated-user")
	req.Header.Set("X-Authentik-Email", "private@example.invalid")
	req.Header.Set("X-Client-Cert", "synthetic-client-certificate")
	req.Header.Set("X-Email", "private@example.invalid")
	req.Header.Set("X-Goog-Authenticated-User-Email", "accounts.google.com:private@example.invalid")
	req.Header.Set("X-Goog-IAP-JWT-Assertion", "synthetic-google-identity-jwt")
	req.Header.Set("X-Groups", "synthetic-private-group")
	req.Header.Set("X-Identity-Subject", "synthetic-private-subject")
	req.Header.Set("X-Keycloak-User", "synthetic-keycloak-user")
	req.Header.Set("X-MS-Client-Principal", "synthetic-azure-principal")
	req.Header.Set("X-Pomerium-Claim-Email", "private@example.invalid")
	req.Header.Set("X-Roles", "synthetic-private-role")
	req.Header.Set("X-Spiffe-ID", "spiffe://private.invalid/workload")
	req.Header.Set("X-SSL-Client-Cert", "synthetic-client-certificate")
	req.Header.Set("X-TLS-Client-Cert", "synthetic-client-certificate")
	req.Header.Set("X-User", "synthetic-private-user")
	req.Header.Set("X-User-Email", "private@example.invalid")
	req.Header.Set("X-Envoy-Original-Path", "/api/v3/config/host")
	req.Header.Set("X-HTTP-Method", http.MethodDelete)
	req.Header.Set("X-HTTP-Method-Override", http.MethodDelete)
	req.Header.Set("X-Method-Override", http.MethodDelete)
	req.Header.Set("X-Original-URL", "/api/v3/config/host")
	req.Header.Set("X-Rewrite-URL", "/api/v3/config/host")
	req.Header.Set("X-Request-ID", "safe-request-id")
	req.Trailer = http.Header{
		"Authorization": []string{"Bearer " + trailerSecret},
		"X-Api-Key":     []string{trailerSecret},
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, []byte(`{"ok":true}`)) {
		t.Fatalf("response status=%d body=%q, want 200 JSON", resp.StatusCode, body)
	}
}

// SEC-004: client protocol-upgrade metadata cannot turn the HTTP proxy into an opaque tunnel.
func TestInstanceProxyDropsProtocolUpgradeRequest(t *testing.T) {
	proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, name := range []string{
			"Connection",
			"Upgrade",
			"HTTP2-Settings",
			"Sec-WebSocket-Extensions",
			"Sec-WebSocket-Key",
			"Sec-WebSocket-Protocol",
			"Sec-WebSocket-Version",
		} {
			if values := r.Header.Values(name); len(values) != 0 {
				t.Errorf("upstream received protocol-upgrade header %s=%q", name, values)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))

	req, err := http.NewRequest(http.MethodGet, proxyURL+"/api/instances/"+instanceID+"/api/v3/movie", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Connection", "Upgrade, HTTP2-Settings")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("HTTP2-Settings", "synthetic-settings")
	req.Header.Set("Sec-WebSocket-Extensions", "permessage-deflate")
	req.Header.Set("Sec-WebSocket-Key", "synthetic-websocket-key")
	req.Header.Set("Sec-WebSocket-Protocol", "synthetic-protocol")
	req.Header.Set("Sec-WebSocket-Version", "13")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, []byte(`{"ok":true}`)) {
		t.Fatalf("response status=%d body=%q, want 200 JSON", resp.StatusCode, body)
	}
}

// SEC-004: CONNECT is rejected before instance resolution or an upstream round trip.
func TestInstanceProxyRejectsConnectBeforeInstanceLookupOrUpstreamContact(t *testing.T) {
	t.Run("nil store is never dereferenced", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodConnect, "/api/instances/synthetic/tunnel", nil)
		NewHandler(nil).InstanceProxy().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status=%d body=%q, want 405", recorder.Code, recorder.Body.Bytes())
		}
		assertPrivateProxyCachePolicy(t, recorder.Header())
	})

	contacted := make(chan struct{}, 1)
	proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		contacted <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, id := range []string{instanceID, "synthetic-missing-instance"} {
		t.Run(id, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodConnect, proxyURL+"/api/instances/"+id+"/tunnel", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("CONNECT: %v", err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("status=%d body=%q, want 405", resp.StatusCode, body)
			}
			if got := resp.Header.Get("Cache-Control"); got != "private, no-store" {
				t.Errorf("Cache-Control=%q, want private, no-store", got)
			}
			select {
			case <-contacted:
				t.Fatal("CONNECT reached the configured upstream")
			default:
			}
		})
	}
}

// SEC-004: TRACE and TRACK cannot reflect the proxy-injected instance API key.
func TestInstanceProxyRejectsTraceAndTrackBeforeInstanceLookupOrUpstreamContact(t *testing.T) {
	for _, method := range []string{http.MethodTrace, "TRACK", "trace", "track"} {
		t.Run(method+" nil store", func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(method, "/api/instances/synthetic/reflect", nil)
			NewHandler(nil).InstanceProxy().ServeHTTP(recorder, request)
			if recorder.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status=%d body=%q, want 405", recorder.Code, recorder.Body.Bytes())
			}
			assertPrivateProxyCachePolicy(t, recorder.Header())
		})
	}

	var contacted atomic.Int64
	proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contacted.Add(1)
		_, _ = io.WriteString(w, r.Header.Get("X-Api-Key"))
	}))

	for _, method := range []string{http.MethodTrace, "TRACK", "trace", "track"} {
		t.Run(method+" configured instance", func(t *testing.T) {
			req, err := http.NewRequest(method, proxyURL+"/api/instances/"+instanceID+"/reflect", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("%s: %v", method, err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("status=%d body=%q, want 405", resp.StatusCode, body)
			}
			if bytes.Contains(body, []byte("test-instance-api-key")) {
				t.Fatalf("blocked reflection method exposed configured API key: %q", body)
			}
			assertPrivateProxyCachePolicy(t, resp.Header)
		})
	}
	if got := contacted.Load(); got != 0 {
		t.Fatalf("TRACE/TRACK contacted configured upstream %d times", got)
	}
}

// SEC-004: an upstream 101 response is rejected before opaque tunnel bytes can reach the client.
func TestInstanceProxyRejectsUpstreamProtocolUpgrade(t *testing.T) {
	const tunnelSecret = "synthetic-upgrade-tunnel-secret"
	proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("upstream response writer does not support hijacking")
			return
		}
		conn, rw, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack upstream connection: %v", err)
			return
		}
		defer conn.Close()
		_, _ = io.WriteString(rw,
			"HTTP/1.1 101 Switching Protocols\r\n"+
				"Connection: Upgrade\r\n"+
				"Upgrade: websocket\r\n"+
				"X-Api-Key: "+tunnelSecret+"\r\n\r\n"+
				tunnelSecret,
		)
		_ = rw.Flush()
	}))

	req, err := http.NewRequest(http.MethodGet, proxyURL+"/api/instances/"+instanceID+"/api/v3/movie", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("response status=%d body=%q, want 502", resp.StatusCode, body)
	}
	if bytes.Contains(body, []byte(tunnelSecret)) || resp.Header.Get("X-Api-Key") != "" {
		t.Fatalf("upgrade response exposed tunnel bytes or headers: header=%q body=%q", resp.Header.Get("X-Api-Key"), body)
	}
}

func TestInstanceProxyDropsUpstreamResponseTrailers(t *testing.T) {
	const trailerSecret = "synthetic-response-trailer-secret"
	proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Add("Trailer", "X-Api-Key")
		w.Header().Add("Trailer", "X-Download-URL")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "safe opaque body")
		w.Header().Set("X-Api-Key", trailerSecret)
		w.Header().Set("X-Download-URL", "https://user:"+trailerSecret+"@download.invalid/file?token="+trailerSecret)
	}))

	resp, err := http.Get(proxyURL + "/api/instances/" + instanceID + "/download")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if string(body) != "safe opaque body" {
		t.Fatalf("body=%q, want safe opaque body", body)
	}
	if len(resp.Trailer) != 0 || len(resp.Header.Values("Trailer")) != 0 {
		t.Fatalf("upstream response trailers escaped boundary: header=%v trailer=%v", resp.Header.Values("Trailer"), resp.Trailer)
	}
	if bytes.Contains(body, []byte(trailerSecret)) {
		t.Fatal("response body unexpectedly contains trailer secret")
	}
}

func TestInstanceProxyStripsUpstreamProxyControlAndCacheHeaders(t *testing.T) {
	proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		addUpstreamControlHeaders(w.Header())
		addUpstreamSharedCacheHeaders(w.Header())
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"safe":true}`)
	}))

	resp, err := http.Get(proxyURL + "/api/instances/" + instanceID + "/api/v3/history")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, []byte(`{"safe":true}`)) {
		t.Fatalf("status=%d body=%q, want safe JSON", resp.StatusCode, body)
	}
	assertNoUpstreamControlHeaders(t, resp.Header)
	assertPrivateProxyCachePolicy(t, resp.Header)
}

func TestInstanceProxyErrorStripsUpstreamProxyControlAndCacheHeaders(t *testing.T) {
	proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		addUpstreamControlHeaders(w.Header())
		addUpstreamSharedCacheHeaders(w.Header())
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"apiKey":"synthetic-invalid-json"`)
	}))

	resp, err := http.Get(proxyURL + "/api/instances/" + instanceID + "/api/v3/history")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status=%d body=%q, want 502", resp.StatusCode, body)
	}
	assertNoUpstreamControlHeaders(t, resp.Header)
	assertPrivateProxyCachePolicy(t, resp.Header)
}

func TestInstanceProxySanitizesEarlyHintsBeforeForwarding(t *testing.T) {
	const earlySecret = "synthetic-early-hint-secret"
	proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		addUpstreamControlHeaders(w.Header())
		addUpstreamSharedCacheHeaders(w.Header())
		w.Header().Set("Link", `<https://user:`+earlySecret+`@assets.invalid/app.js?safe=1&token=`+earlySecret+`>; rel="preload"; as="script"; integrity="`+earlySecret+`"; rel="https://user:`+earlySecret+`@relation.invalid/?token=`+earlySecret+`"`)
		w.Header().Set("X-Api-Key", earlySecret)
		w.Header().Set("X-Download-URL", "https://user:"+earlySecret+"@download.invalid/file?apiKey="+earlySecret)
		w.WriteHeader(http.StatusEarlyHints)
		w.Header().Del("Link")
		w.Header().Del("X-Api-Key")
		w.Header().Del("X-Download-URL")
		for _, name := range upstreamControlHeaderNames() {
			w.Header().Del(name)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))

	var informational []textproto.MIMEHeader
	trace := &httptrace.ClientTrace{Got1xxResponse: func(code int, header textproto.MIMEHeader) error {
		if code == http.StatusEarlyHints {
			informational = append(informational, header)
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(
		httptrace.WithClientTrace(context.Background(), trace),
		http.MethodGet,
		proxyURL+"/api/instances/"+instanceID+"/api/v3/history",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, []byte(`{"ok":true}`)) {
		t.Fatalf("final response status=%d body=%q", resp.StatusCode, body)
	}
	if len(informational) != 1 {
		t.Fatalf("early hint responses=%d, want 1", len(informational))
	}
	hint := http.Header(informational[0])
	assertNoUpstreamControlHeaders(t, hint)
	assertNoSharedCacheHeaders(t, hint)
	encoded := hint.Get("Link") + hint.Get("X-Api-Key") + hint.Get("X-Download-URL")
	if strings.Contains(encoded, earlySecret) || strings.Contains(encoded, "user:") {
		t.Fatalf("early hints exposed credential material: %#v", hint)
	}
	if got := hint.Get("Link"); !strings.Contains(got, "https://assets.invalid/app.js?safe=1") || !strings.Contains(got, `rel="preload"`) || !strings.Contains(got, `as="script"`) {
		t.Fatalf("sanitized safe Link metadata=%q", got)
	}
	if hint.Get("X-Api-Key") != "" || hint.Get("X-Download-URL") != "" {
		t.Fatalf("unsafe early-hint extension headers survived: %#v", hint)
	}
}
