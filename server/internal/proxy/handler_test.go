package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// TestInstanceProxyForwardsChaptarrLookup is a smoke test for the Books-tab
// search path: GET /api/instances/{id}/api/v1/book/lookup?term=... must reach
// the Chaptarr upstream verbatim — prefix stripped, query string preserved, and
// the instance's X-Api-Key injected — with the response content passed back.
func TestInstanceProxyForwardsChaptarrLookup(t *testing.T) {
	var gotPath, gotTerm, gotKey string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotTerm = r.URL.Query().Get("term")
		gotKey = r.Header.Get("X-Api-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"title":"Dune","foreignBookId":"gr:234","author":{"id":0,"authorName":"Frank Herbert"}}]`))
	}))
	defer upstream.Close()

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	store := instance.NewStore(database, cipher)
	inst := &instance.Instance{
		ServiceType: "chaptarr",
		Name:        "Books",
		URL:         upstream.URL,
		APIKey:      "secret-key",
	}
	if err := store.Create(inst); err != nil {
		t.Fatalf("create instance: %v", err)
	}

	r := chi.NewRouter()
	r.HandleFunc("/api/instances/{instanceID}/*", NewHandler(store).InstanceProxy())
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/instances/" + inst.ID + "/api/v1/book/lookup?term=dune")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	if gotPath != "/api/v1/book/lookup" {
		t.Errorf("upstream path = %q, want /api/v1/book/lookup", gotPath)
	}
	if gotTerm != "dune" {
		t.Errorf("upstream term = %q, want \"dune\" (query string dropped?)", gotTerm)
	}
	if gotKey != "secret-key" {
		t.Errorf("upstream X-Api-Key = %q, want \"secret-key\"", gotKey)
	}
	if !bytes.Contains(body, []byte("Dune")) {
		t.Errorf("response not passed through verbatim: %s", body)
	}
}

func startTestProxy(t *testing.T, upstream http.Handler) (string, string) {
	return startTestProxyWithBasePath(t, upstream, "")
}

func startTestProxyWithBasePath(t *testing.T, upstream http.Handler, basePath string) (string, string) {
	t.Helper()
	upstreamServer := httptest.NewServer(upstream)
	t.Cleanup(upstreamServer.Close)

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	store := instance.NewStore(database, cipher)
	inst := &instance.Instance{
		ServiceType: "sonarr",
		Name:        "TV",
		URL:         upstreamServer.URL + basePath,
		APIKey:      "test-instance-api-key",
	}
	if err := store.Create(inst); err != nil {
		t.Fatalf("create instance: %v", err)
	}

	router := chi.NewRouter()
	router.HandleFunc("/api/instances/{instanceID}/*", NewHandler(store).InstanceProxy())
	proxyServer := httptest.NewServer(router)
	t.Cleanup(proxyServer.Close)
	return proxyServer.URL, inst.ID
}

// SEC-004: Cantinarr credentials are stripped before upstream auth is applied.
func TestInstanceProxyStripsCantinarrCredentialsBeforeUpstream(t *testing.T) {
	var clientFacingHost string
	proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, name := range []string{
			"Authorization",
			"Cf-Connecting-Ip",
			"Client-Ip",
			"Connection",
			"Cookie",
			"Cookie2",
			"Fastly-Client-Ip",
			"Fly-Client-Ip",
			"Forwarded",
			"Proxy-Authorization",
			"True-Client-Ip",
			"Via",
			"X-Cluster-Client-Ip",
			"X-Connection-Only-Secret",
			"X-Envoy-External-Address",
			"X-Forwarded",
			"X-Forwarded-For",
			"X-Forwarded-Host",
			"X-Forwarded-Port",
			"X-Forwarded-Proto",
			"X-Original-Forwarded-For",
			"X-Real-Ip",
			"X-Session-Token",
		} {
			if got := r.Header.Values(name); len(got) != 0 {
				t.Errorf("upstream received %s: %q", name, got)
			}
		}
		if got := r.Header.Get("X-Api-Key"); got != "test-instance-api-key" {
			t.Errorf("upstream X-Api-Key = %q, want stored instance key", got)
		}
		if got := r.Header.Get("Accept-Encoding"); got != "identity" {
			t.Errorf("upstream Accept-Encoding = %q, want identity", got)
		}
		if got := r.Header.Get("X-Request-Id"); got != "safe-correlation-id" {
			t.Errorf("upstream X-Request-Id = %q, want preserved safe header", got)
		}
		if got := r.URL.Path; got != "/api/v3/system/status" {
			t.Errorf("upstream path = %q", got)
		}
		if got := r.URL.Query().Get("term"); got != "dune" {
			t.Errorf("upstream safe query = %q, want dune", got)
		}
		if got := r.URL.Query().Get("page"); got != "2" {
			t.Errorf("upstream page query = %q, want 2", got)
		}
		if r.Host == "" || r.Host == clientFacingHost {
			t.Errorf("upstream Host = %q, want configured upstream host", r.Host)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	parsedProxyURL, err := url.Parse(proxyURL)
	if err != nil {
		t.Fatal(err)
	}
	clientFacingHost = parsedProxyURL.Host
	req, err := http.NewRequest(http.MethodGet, proxyURL+"/api/instances/"+instanceID+"/api/v3/system/status?term=dune&page=2", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer cantinarr-jwt-secret")
	req.Header.Set("Cookie", "session=cantinarr-cookie-secret")
	req.Header.Add("Cookie", "second=cantinarr-cookie-secret")
	req.Header.Set("Cookie2", "session=cantinarr-cookie2-secret")
	req.Header.Set("Forwarded", "for=192.0.2.1;host=client.invalid;proto=https")
	req.Header.Add("Forwarded", "for=192.0.2.2")
	req.Header.Set("CF-Connecting-IP", "192.0.2.6")
	req.Header.Set("Client-IP", "192.0.2.7")
	req.Header.Set("Fastly-Client-IP", "192.0.2.8")
	req.Header.Set("Fly-Client-IP", "192.0.2.9")
	req.Header.Set("True-Client-IP", "192.0.2.10")
	req.Header.Set("Via", "1.1 internal-proxy.invalid")
	req.Header.Set("X-Cluster-Client-IP", "192.0.2.11")
	req.Header.Set("X-Envoy-External-Address", "192.0.2.12")
	req.Header.Set("X-Forwarded", "for=192.0.2.3")
	req.Header.Set("X-Forwarded-For", "192.0.2.4")
	req.Header.Set("X-Forwarded-Host", "client.invalid")
	req.Header.Set("X-Forwarded-Port", "443")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Original-Forwarded-For", "192.0.2.13")
	req.Header.Set("X-Real-IP", "192.0.2.5")
	req.Header.Set("X-Session-Token", "custom-secret")
	req.Header.Set("Proxy-Authorization", "Basic proxy-secret")
	req.Header.Set("X-Api-Key", "attacker-controlled-api-key")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("X-Request-Id", "safe-correlation-id")
	req.Header.Set("X-Connection-Only-Secret", "connection-nominated-secret")
	// These hop-by-hop nominations previously removed the stored API key and
	// identity encoding after Director injected them.
	req.Header.Set("Connection", "X-Api-Key, Accept-Encoding, X-Connection-Only-Secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestJoinURLPathPreservesInstanceBasePath(t *testing.T) {
	if got := joinURLPath("/sonarr/", "/api/v3/queue"); got != "/sonarr/api/v3/queue" {
		t.Fatalf("joinURLPath = %q", got)
	}
}

// INST-018: Nested proxy JSON and secret-bearing URLs are recursively scrubbed.
func TestInstanceProxyRedactsNestedSecretsAndURLQueries(t *testing.T) {
	const (
		objectSecret        = "object-secret-value"
		querySecret         = "query-secret-value"
		tokenSecret         = "token-secret-value"
		authorizationSecret = "authorization-secret-value"
		cookieSecret        = "cookie-secret-value"
		encodedQuerySecret  = "encoded-query-secret-value"
		repeatedQuerySecret = "repeated-query-secret-value"
		userinfoName        = "url-user-secret"
		userinfoPassword    = "url-password-secret"
	)
	proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept-Encoding"); got != "identity" {
			t.Errorf("upstream Accept-Encoding = %q, want identity", got)
		}
		w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
		w.Header().Set("X-Api-Key", objectSecret)
		w.Header().Set("Location", "https://indexer.invalid/item?id=7&apiKey="+querySecret)
		_, _ = w.Write([]byte(`{
			"page": 1,
			"records": [{
				"data": {
					"apiKey": "` + objectSecret + `",
					"PASSWORD": "` + objectSecret + `",
					"downloadUrl": "https://indexer.invalid/download?id=7&apiKey=` + querySecret + `&token=` + tokenSecret + `#result",
					"ordinaryKey": "kept"
				},
				"nested": [
					{"access_token": "` + tokenSecret + `"},
					{"name": "apiKey", "value": "` + querySecret + `"},
					{
						"AuThOrIzAtIoN": "Bearer ` + authorizationSecret + `",
						"cOoKiE": "session=` + cookieSecret + `",
						"redirectUrl": "https://` + userinfoName + `:` + userinfoPassword + `@indexer.invalid/next?safe=kept&API%4bEY=` + encodedQuerySecret + `&ToKeN=` + tokenSecret + `&token=` + repeatedQuerySecret + `#result",
						"ordinaryField": "preserved"
					}
				]
			}]
		}`))
	}))

	resp, err := http.Get(proxyURL + "/api/instances/" + instanceID + "/api/v3/history")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	for _, secret := range []string{
		objectSecret,
		querySecret,
		tokenSecret,
		authorizationSecret,
		cookieSecret,
		encodedQuerySecret,
		repeatedQuerySecret,
		userinfoName,
		userinfoPassword,
	} {
		if bytes.Contains(body, []byte(secret)) {
			t.Fatal("proxied response retained a synthetic secret")
		}
	}
	if got := resp.Header.Get("X-Api-Key"); got != "" {
		t.Errorf("sensitive response header was retained")
	}
	if got := resp.Header.Get("Location"); strings.Contains(got, querySecret) || !strings.Contains(got, "id=7") {
		t.Errorf("Location header was not safely redacted: %q", got)
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode sanitized response: %v", err)
	}
	records := decoded["records"].([]any)
	record := records[0].(map[string]any)
	data := record["data"].(map[string]any)
	if data["apiKey"] != redactedValue || data["PASSWORD"] != redactedValue {
		t.Errorf("sensitive object fields were not redacted")
	}
	if data["ordinaryKey"] != "kept" {
		t.Errorf("ordinary value = %v, want kept", data["ordinaryKey"])
	}
	downloadURL := data["downloadUrl"].(string)
	if !strings.Contains(downloadURL, "id=7") || strings.Count(downloadURL, url.QueryEscape(redactedValue)) != 2 {
		t.Errorf("download URL query was not selectively redacted: %q", downloadURL)
	}
	nested := record["nested"].([]any)[0].(map[string]any)
	if nested["access_token"] != redactedValue {
		t.Errorf("nested token = %v, want redacted", nested["access_token"])
	}
	keyValue := record["nested"].([]any)[1].(map[string]any)
	if keyValue["value"] != redactedValue {
		t.Errorf("dynamic key/value credential was not redacted")
	}
	credentials := record["nested"].([]any)[2].(map[string]any)
	if credentials["AuThOrIzAtIoN"] != redactedValue || credentials["cOoKiE"] != redactedValue {
		t.Errorf("nested authorization/cookie fields were not redacted: %v", credentials)
	}
	if credentials["ordinaryField"] != "preserved" {
		t.Errorf("ordinary nested field = %v, want preserved", credentials["ordinaryField"])
	}
	redirectURL := credentials["redirectUrl"].(string)
	if !strings.Contains(redirectURL, "https://indexer.invalid/next?safe=kept") ||
		!strings.HasSuffix(redirectURL, "#result") ||
		strings.Count(redirectURL, url.QueryEscape(redactedValue)) != 3 {
		t.Errorf("nested URL credentials were not selectively redacted: %q", redirectURL)
	}
}

func TestInstanceProxySanitizesJSONWithUnreliableContentType(t *testing.T) {
	const syntheticSecret = "synthetic-content-type-secret"
	tests := []struct {
		name         string
		contentTypes []string
		wantStatus   int
	}{
		{"mislabelled text", []string{"text/plain"}, http.StatusOK},
		{"json is second header", []string{"text/plain", "application/json"}, http.StatusOK},
		{"combined header", []string{"text/plain, application/problem+json"}, http.StatusOK},
		{"malformed json type", []string{"application/json; charset"}, http.StatusBadGateway},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for _, contentType := range tt.contentTypes {
					w.Header().Add("Content-Type", contentType)
				}
				_, _ = w.Write([]byte(` {"apiKey":"` + syntheticSecret + `","safe":"kept"}`))
			}))

			resp, err := http.Get(proxyURL + "/api/instances/" + instanceID + "/api/v3/history")
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", resp.StatusCode, tt.wantStatus, body)
			}
			if bytes.Contains(body, []byte(syntheticSecret)) {
				t.Fatal("unreliably typed JSON retained a synthetic secret")
			}
			if tt.wantStatus == http.StatusOK && !bytes.Contains(body, []byte(`"safe":"kept"`)) {
				t.Errorf("safe JSON data was not preserved: %s", body)
			}
		})
	}
}

func TestInstanceProxySanitizesMislabelledJSONBehindInstanceBasePath(t *testing.T) {
	const syntheticSecret = "base-path-api-key-secret"
	proxyURL, instanceID := startTestProxyWithBasePath(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sonarr/api/v3/history" {
			t.Errorf("upstream path = %q, want /sonarr/api/v3/history", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(`{"apiKey":"` + syntheticSecret + `","safe":"kept"}`))
	}), "/sonarr")

	resp, err := http.Get(proxyURL + "/api/instances/" + instanceID + "/api/v3/history")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	if bytes.Contains(body, []byte(syntheticSecret)) || !bytes.Contains(body, []byte(`"safe":"kept"`)) {
		t.Fatalf("base-path response was not safely sanitized: %s", body)
	}
}

func TestInstanceProxySanitizesMislabelledJSONErrorOnOpaqueCoverRoute(t *testing.T) {
	const syntheticSecret = "synthetic-cover-error-secret"
	proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/API/V3/MEDIACOVER/Movies/1/poster.jpg" {
			t.Errorf("upstream path = %q", got)
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, `{"apiKey":"`+syntheticSecret+`","safe":"kept"}`)
	}))

	resp, err := http.Get(proxyURL + "/api/instances/" + instanceID + "/API/V3/MEDIACOVER/Movies/1/poster.jpg")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	if bytes.Contains(body, []byte(syntheticSecret)) || !bytes.Contains(body, []byte(`"safe":"kept"`)) || !bytes.Contains(body, []byte(redactedValue)) {
		t.Fatalf("opaque-route JSON error was not scrubbed: %s", body)
	}
}

// SEC-007: Wrong-route SSE and structured JSON streams fail closed without reflecting secrets.
func TestInstanceProxyRejectsUnsanitizableStreamsWithoutReflectingThem(t *testing.T) {
	tests := []struct {
		name         string
		contentTypes []string
	}{
		{"server-sent events", []string{"text/event-stream"}},
		{"server-sent events with parameters", []string{"text/event-stream; charset=utf-8"}},
		{"malformed server-sent events", []string{"text/event-stream; charset"}},
		{"mixed server-sent events and json", []string{"text/event-stream", "application/json"}},
		{"x-ndjson", []string{"application/x-ndjson"}},
		{"ndjson", []string{"application/ndjson"}},
		{"json sequence", []string{"application/json-seq"}},
		{"stream json", []string{"application/stream+json"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const syntheticSecret = "streaming-response-secret"
			proxyURL, instanceID := startTestProxyWithBasePath(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for _, contentType := range tt.contentTypes {
					w.Header().Add("Content-Type", contentType)
				}
				_, _ = w.Write([]byte("data: {\"apiKey\":\"" + syntheticSecret + "\"}\n\n"))
			}), "/sonarr")

			resp, err := http.Get(proxyURL + "/api/instances/" + instanceID + "/api/v3/history")
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502; body=%s", resp.StatusCode, body)
			}
			if bytes.Contains(body, []byte(syntheticSecret)) {
				t.Fatal("error response reflected unsafe streaming bytes")
			}
		})
	}
}

// SEC-007: Intended arr SSE is scrubbed event-by-event and remains unbuffered.
func TestInstanceProxySanitizesIntendedSSEStream(t *testing.T) {
	const (
		firstSecret  = "synthetic-first-sse-secret"
		secondSecret = "synthetic-second-sse-secret"
		userinfo     = "synthetic-sse-user"
		password     = "synthetic-sse-password"
	)
	releaseSecondEvent := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(releaseSecondEvent)
		}
	}()

	proxyURL, instanceID := startTestProxyWithBasePath(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/sonarr/api/v3/events" {
			t.Errorf("upstream path = %q, want /sonarr/api/v3/events", got)
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("ETag", `"unsafe-upstream-representation"`)
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("CDN-Cache-Control", "public, s-maxage=86400")
		w.Header().Set("Content-Digest", "sha-256=:c3ludGhldGlj:")
		w.Header().Set("Content-Range", "bytes 0-9/10")
		w.Header().Set("Expires", "Fri, 17 Jul 2037 00:00:00 GMT")
		w.Header().Set("Last-Modified", "Fri, 17 Jul 2026 00:00:00 GMT")
		w.Header().Set("Repr-Digest", "sha-256=:c3ludGhldGlj:")
		w.Header().Set("Surrogate-Control", "public, max-age=86400")
		_, _ = io.WriteString(w,
			"\ufeff: "+firstSecret+"\r\n"+
				"event: queue.updated\r\n"+
				"id: cursor-42\r\n"+
				"retry: 1500\r\n"+
				"untrusted-extension: "+firstSecret+"\r\n"+
				`data: {"apiKey":"`+firstSecret+`","nested":{"Authorization":"Bearer `+firstSecret+`"},"downloadUrl":"https://`+userinfo+`:`+password+`@download.invalid/file?safe=kept&token=`+firstSecret+`","safe":"kept"}`+"\r\n\r\n",
		)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("upstream response writer does not support flushing")
			return
		}
		flusher.Flush()
		select {
		case <-releaseSecondEvent:
			_, _ = io.WriteString(w,
				`event: queue.completed`+"\n"+
					`id: https://`+userinfo+`:`+password+`@download.invalid/cursor?safe=second&token=`+secondSecret+"\n"+
					`data: {"password":"`+secondSecret+`","safe":"second"}`+"\n\n",
			)
			flusher.Flush()
		case <-r.Context().Done():
		}
	}), "/sonarr")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(proxyURL + "/api/instances/" + instanceID + "/api/v3/events")
	if err != nil {
		t.Fatalf("GET before second event was released: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want event stream", got)
	}
	for _, name := range []string{"Accept-Ranges", "Content-Digest", "Content-Range", "ETag", "Repr-Digest"} {
		if got := resp.Header.Get(name); got != "" {
			t.Errorf("rewritten stream retained upstream %s %q", name, got)
		}
	}
	if got := resp.Header.Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("rewritten stream Cache-Control = %q, want private, no-store", got)
	}
	if got := resp.Header.Get("Pragma"); got != "no-cache" {
		t.Errorf("rewritten stream Pragma = %q, want no-cache", got)
	}
	for _, name := range []string{"CDN-Cache-Control", "Expires", "Last-Modified", "Surrogate-Control"} {
		if got := resp.Header.Get(name); got != "" {
			t.Errorf("rewritten stream retained upstream cache header %s=%q", name, got)
		}
	}

	reader := bufio.NewReader(resp.Body)
	first := readSSEFrame(t, reader)
	for _, secret := range []string{firstSecret, userinfo, password} {
		if strings.Contains(first, secret) {
			t.Fatalf("first sanitized event retained %q: %q", secret, first)
		}
	}
	for _, safe := range []string{
		"event: queue.updated\n",
		"id: cursor-42\n",
		"retry: 1500\n",
		`"safe":"kept"`,
		"https://download.invalid/file?safe=kept",
		redactedValue,
	} {
		if !strings.Contains(first, safe) {
			t.Errorf("first event lost safe/sanitized value %q: %q", safe, first)
		}
	}
	if strings.Contains(first, "untrusted-extension") {
		t.Errorf("unknown SSE field survived: %q", first)
	}

	close(releaseSecondEvent)
	released = true
	second := readSSEFrame(t, reader)
	if strings.Contains(second, secondSecret) || strings.Contains(second, userinfo) || strings.Contains(second, password) ||
		!strings.Contains(second, `"safe":"second"`) || !strings.Contains(second, "id: https://download.invalid/cursor?safe=second") ||
		!strings.Contains(second, redactedValue) {
		t.Fatalf("second event was not safely streamed: %q", second)
	}
	if rest, err := io.ReadAll(reader); err != nil || len(rest) != 0 {
		t.Fatalf("stream tail = %q, err=%v; want clean EOF", rest, err)
	}
}

func readSSEFrame(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	var frame strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE frame: %v; partial=%q", err, frame.String()+line)
		}
		frame.WriteString(line)
		if line == "\n" {
			return frame.String()
		}
	}
}

// SEC-007: Ambiguous non-JSON arr API bodies cannot masquerade as safe opaque streams.
func TestInstanceProxyRejectsAmbiguousArrAPIStreams(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{
			name:        "mislabelled SSE",
			contentType: "text/plain",
			body:        "data: {\"apiKey\":\"synthetic-ambiguous-secret\"}\n\n",
		},
		{
			name: "missing type SSE",
			body: "data: {\"apiKey\":\"synthetic-ambiguous-secret\"}\n\n",
		},
		{
			name:        "XSSI-prefixed JSON",
			contentType: "application/octet-stream",
			body:        ")]}'\n{\"apiKey\":\"synthetic-ambiguous-secret\"}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.contentType != "" {
					w.Header().Set("Content-Type", tt.contentType)
				}
				_, _ = io.WriteString(w, tt.body)
			}))

			resp, err := http.Get(proxyURL + "/api/instances/" + instanceID + "/api/v3/history")
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502; body=%s", resp.StatusCode, body)
			}
			if bytes.Contains(body, []byte("synthetic-ambiguous-secret")) {
				t.Fatal("error response reflected ambiguous upstream bytes")
			}
		})
	}
}

// SEC-007: Malformed or oversized first SSE events fail before a 200 response commits.
func TestInstanceProxyRejectsUnsafeIntendedSSEBeforeCommitting(t *testing.T) {
	const sentinel = "synthetic-unsafe-sse-secret"
	tests := []struct {
		name string
		body []byte
	}{
		{name: "partial event", body: []byte(`data: {"apiKey":"` + sentinel + `"}`)},
		{name: "invalid retry", body: []byte("retry: nope\ndata: {\"apiKey\":\"" + sentinel + "\"}\n\n")},
		{name: "invalid UTF-8", body: []byte{'d', 'a', 't', 'a', ':', ' ', 0xff, '\n', '\n'}},
		{name: "oversized event", body: []byte("data: " + sentinel + strings.Repeat("x", maxSanitizedSSEEventSize) + "\n\n")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write(tt.body)
			}))

			resp, err := http.Get(proxyURL + "/api/instances/" + instanceID + "/api/v3/events")
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502; body=%s", resp.StatusCode, body)
			}
			if bytes.Contains(body, []byte(sentinel)) {
				t.Fatal("error response reflected unsafe first event")
			}
		})
	}
}

// SEC-007: Legitimate opaque text and binary streams pass through unbuffered.
func TestInstanceProxyPassesNonJSONStreamUnchanged(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		contentType string
		first       []byte
		second      []byte
	}{
		{
			name:        "binary download",
			path:        "/download",
			contentType: "application/octet-stream",
			first:       []byte{0x00, 0x01},
			second:      []byte{0xfe, 0xff, 'x'},
		},
		{
			name:        "arr log text",
			path:        "/api/v3/log/file/server.txt",
			contentType: "text/plain; charset=utf-8",
			first:       []byte("first log line\n"),
			second:      []byte("second log line\n"),
		},
		{
			name:        "arr cover image",
			path:        "/API/V3/MEDIACOVER/Movies/1/poster.jpg",
			contentType: "application/octet-stream",
			first:       []byte{0x89, 'P', 'N', 'G', '\r', '\n'},
			second:      []byte{0x1a, '\n', 0x00, 0xff},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			releaseSecondChunk := make(chan struct{})
			released := false
			defer func() {
				if !released {
					close(releaseSecondChunk)
				}
			}()

			proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				w.Header().Set("Cache-Control", "public, max-age=86400")
				w.Header().Set("CDN-Cache-Control", "public, s-maxage=86400")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(tt.first)
				flusher, ok := w.(http.Flusher)
				if !ok {
					t.Error("upstream response writer does not support flushing")
					return
				}
				flusher.Flush()
				select {
				case <-releaseSecondChunk:
					_, _ = w.Write(tt.second)
				case <-r.Context().Done():
				}
			}))

			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Get(proxyURL + "/api/instances/" + instanceID + tt.path)
			if err != nil {
				t.Fatalf("GET before upstream released second chunk: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			if got := resp.Header.Get("Cache-Control"); got != "private, no-store" {
				t.Errorf("opaque stream Cache-Control = %q, want private, no-store", got)
			}
			if got := resp.Header.Get("CDN-Cache-Control"); got != "" {
				t.Errorf("opaque stream retained shared-cache policy %q", got)
			}

			first := make([]byte, len(tt.first))
			if _, err := io.ReadFull(resp.Body, first); err != nil {
				t.Fatalf("read flushed first chunk: %v", err)
			}
			if !bytes.Equal(first, tt.first) {
				t.Fatalf("first chunk = %v, want %v", first, tt.first)
			}

			close(releaseSecondChunk)
			released = true
			second, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read second chunk: %v", err)
			}
			if !bytes.Equal(second, tt.second) {
				t.Fatalf("second chunk = %v, want %v", second, tt.second)
			}
		})
	}
}

func TestInstanceProxyRejectsUnsafeJSONWithoutReflectingIt(t *testing.T) {
	t.Run("invalid", func(t *testing.T) {
		const sentinel = "malformed-secret-sentinel"
		proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"apiKey":"` + sentinel + `"`))
		}))
		resp, err := http.Get(proxyURL + "/api/instances/" + instanceID + "/api/v3/history")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", resp.StatusCode)
		}
		if bytes.Contains(body, []byte(sentinel)) {
			t.Fatal("error response reflected unsafe upstream bytes")
		}
	})

	t.Run("oversized", func(t *testing.T) {
		proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", strconv.FormatInt(maxSanitizedJSONResponseSize+1, 10))
			w.WriteHeader(http.StatusOK)
		}))
		resp, err := http.Get(proxyURL + "/api/instances/" + instanceID + "/api/v3/history")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", resp.StatusCode)
		}
	})
}

// TestInstanceProxySingleContentType guards against duplicated Content-Type
// headers. The /api router pre-sets "application/json" on every response via
// middleware.SetHeader, and ReverseProxy *appends* the upstream's own header
// rather than replacing it. Browsers merge duplicates into
// "application/json, application/json; charset=utf-8", which Dio on Flutter
// web can't recognize as JSON — every arr screen in the web app then fails
// with a TypeError while native clients (which pick one value) work fine.
func TestInstanceProxySingleContentType(t *testing.T) {
	const upstreamContentType = "application/json; charset=utf-8"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", upstreamContentType)
		_, _ = w.Write([]byte(`[{"title":"Attack of the Clones"}]`))
	}))
	defer upstream.Close()

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	store := instance.NewStore(database, cipher)
	inst := &instance.Instance{
		ServiceType: "radarr",
		Name:        "Movies",
		URL:         upstream.URL,
		APIKey:      "secret-key",
	}
	if err := store.Create(inst); err != nil {
		t.Fatalf("create instance: %v", err)
	}

	r := chi.NewRouter()
	// Same default the real /api router applies to every response.
	r.Use(middleware.SetHeader("Content-Type", "application/json"))
	r.HandleFunc("/api/instances/{instanceID}/*", NewHandler(store).InstanceProxy())
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/instances/" + inst.ID + "/api/v3/movie")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	got := resp.Header.Values("Content-Type")
	if len(got) != 1 || got[0] != upstreamContentType {
		t.Errorf("Content-Type = %q, want exactly [%q]", got, upstreamContentType)
	}
}
