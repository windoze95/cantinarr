package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

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
	proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, name := range []string{"Authorization", "Cookie", "X-Session-Token", "Proxy-Authorization"} {
			if got := r.Header.Get(name); got != "" {
				t.Errorf("upstream received %s: %q", name, got)
			}
		}
		if got := r.Header.Get("X-Api-Key"); got != "test-instance-api-key" {
			t.Errorf("upstream X-Api-Key = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	req, err := http.NewRequest(http.MethodGet, proxyURL+"/api/instances/"+instanceID+"/api/v3/system/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer cantinarr-jwt-secret")
	req.Header.Set("Cookie", "session=cantinarr-cookie-secret")
	req.Header.Set("X-Session-Token", "custom-secret")
	req.Header.Set("Proxy-Authorization", "Basic proxy-secret")
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
		objectSecret = "object-secret-value"
		querySecret  = "query-secret-value"
		tokenSecret  = "token-secret-value"
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
					{"name": "apiKey", "value": "` + querySecret + `"}
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
	for _, secret := range []string{objectSecret, querySecret, tokenSecret} {
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

// SEC-007: SSE stays usable while structured JSON streams fail closed.
func TestInstanceProxyPreservesSSEAndRejectsStructuredJSONStreams(t *testing.T) {
	t.Run("server-sent events remain streaming", func(t *testing.T) {
		const want = "event: update\ndata: {\"safe\":true}\n\n"
		proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(want))
		}))

		resp, err := http.Get(proxyURL + "/api/instances/" + instanceID + "/api/v3/events")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || string(body) != want {
			t.Fatalf("SSE response = status %d body %q, want 200 %q", resp.StatusCode, body, want)
		}
	})

	for _, contentType := range []string{
		"application/x-ndjson",
		"application/ndjson",
		"application/json-seq",
		"application/stream+json",
	} {
		t.Run(contentType, func(t *testing.T) {
			const syntheticSecret = "streaming-json-secret"
			proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", contentType)
				_, _ = w.Write([]byte("{\"apiKey\":\"" + syntheticSecret + "\"}\n"))
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
			if bytes.Contains(body, []byte(syntheticSecret)) {
				t.Fatal("error response reflected unsafe streaming JSON bytes")
			}
		})
	}
}

// SEC-007: Intended non-JSON streams pass through unchanged.
func TestInstanceProxyPassesNonJSONStreamUnchanged(t *testing.T) {
	want := []byte{0x00, 0x01, 0xfe, 0xff, 'x'}
	proxyURL, instanceID := startTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(want[:2])
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = w.Write(want[2:])
	}))

	resp, err := http.Get(proxyURL + "/api/instances/" + instanceID + "/download")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(got, want) {
		t.Fatalf("non-JSON stream = status %d body %v, want 200 %v", resp.StatusCode, got, want)
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
