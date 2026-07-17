package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShouldSanitizeJSONContentTypeAndSniffing(t *testing.T) {
	whitespacePrefix := strings.Repeat(" ", maxJSONSniffBytes)
	tests := []struct {
		name            string
		path            string
		contentTypes    []string
		contentEncoding string
		body            string
		wantSanitize    bool
		wantErr         error
	}{
		{
			name:         "explicit json",
			path:         "/api/v3/history",
			contentTypes: []string{"application/json; charset=utf-8"},
			body:         `[{"id":1}]`,
			wantSanitize: true,
		},
		{
			name:         "second content type is json",
			path:         "/api/v3/history",
			contentTypes: []string{"text/plain", "application/problem+json"},
			body:         `{"error":"nope"}`,
			wantSanitize: true,
		},
		{
			name:         "combined content types respect quoted commas",
			path:         "/api/v3/history",
			contentTypes: []string{`application/octet-stream; note="a,b", application/json`},
			body:         `{"id":1}`,
			wantSanitize: true,
		},
		{
			name:         "malformed json media type fails closed",
			path:         "/api/v3/history",
			contentTypes: []string{"application/json; charset"},
			body:         `{"apiKey":"synthetic"}`,
			wantErr:      errMalformedJSONMediaType,
		},
		{
			name:         "unknown json media type fails closed",
			path:         "/api/v3/history",
			contentTypes: []string{"application/x-json"},
			body:         `{"apiKey":"synthetic"}`,
			wantErr:      errMalformedJSONMediaType,
		},
		{
			name:         "missing type arr object is sniffed",
			path:         "/api/v3/history",
			body:         "\n \t{\"id\":1}",
			wantSanitize: true,
		},
		{
			name:         "missing type arr object behind instance base path is sniffed",
			path:         "/sonarr/api/v3/history",
			body:         `{"apiKey":"synthetic"}`,
			wantSanitize: true,
		},
		{
			name:         "case varied arr object is sniffed",
			path:         "/SONARR/API/V3/HISTORY",
			contentTypes: []string{"text/plain"},
			body:         `{"apiKey":"synthetic"}`,
			wantSanitize: true,
		},
		{
			name:         "mislabelled arr array is sniffed",
			path:         "/api/v1/author",
			contentTypes: []string{"text/plain"},
			body:         "  [1,2,3]",
			wantSanitize: true,
		},
		{
			name:         "mislabelled arr scalar is sniffed",
			path:         "/api/v3/synthetic",
			contentTypes: []string{"text/plain"},
			body:         `"https://indexer.invalid/get?apiKey=synthetic"`,
			wantSanitize: true,
		},
		{
			name:         "mislabelled non-api asset remains opaque",
			path:         "/MediaCover/1/poster.jpg",
			contentTypes: []string{"application/octet-stream"},
			body:         `{"not":"an api response"}`,
		},
		{
			name:         "plain arr log remains plain",
			path:         "/api/v3/log/file/server.txt",
			contentTypes: []string{"text/plain"},
			body:         "2026-07-10 ordinary log output\n",
		},
		{
			name:         "mislabelled object error on opaque cover is sniffed",
			path:         "/API/V3/MEDIACOVER/Movies/1/poster.jpg",
			contentTypes: []string{"text/plain"},
			body:         `{"apiKey":"synthetic","safe":"kept"}`,
			wantSanitize: true,
		},
		{
			name:         "genuine case varied opaque cover remains opaque",
			path:         "/API/V3/MEDIACOVER/Movies/1/poster.jpg",
			contentTypes: []string{"application/octet-stream"},
			body:         "synthetic image bytes",
		},
		{
			name:         "intended event stream uses separate sanitizer",
			path:         "/api/v3/events",
			contentTypes: []string{"text/event-stream"},
			body:         "data: {\"id\":1}\n\n",
		},
		{
			name:         "intended event stream with parameters uses separate sanitizer",
			path:         "/sonarr/api/v1/events",
			contentTypes: []string{"text/event-stream; charset=utf-8"},
			body:         "data: {\"id\":1}\n\n",
		},
		{
			name:         "event stream on wrong route fails closed",
			path:         "/api/v3/history",
			contentTypes: []string{"text/event-stream"},
			body:         "data: {\"apiKey\":\"synthetic\"}\n\n",
			wantErr:      errUnsanitizableStream,
		},
		{
			name:         "malformed event stream fails closed",
			path:         "/api/v3/events",
			contentTypes: []string{"text/event-stream; charset"},
			body:         "data: {\"id\":1}\n\n",
			wantErr:      errUnsanitizableStream,
		},
		{
			name:         "event stream mixed with json fails closed",
			path:         "/api/v3/events",
			contentTypes: []string{"text/event-stream", "application/json"},
			body:         "data: {\"id\":1}\n\n",
			wantErr:      errUnsanitizableStream,
		},
		{
			name:         "ndjson fails closed",
			path:         "/api/v3/events",
			contentTypes: []string{"application/x-ndjson"},
			body:         "{\"id\":1}\n{\"id\":2}\n",
			wantErr:      errUnsanitizableStream,
		},
		{
			name:         "ndjson alias fails closed",
			path:         "/api/v3/events",
			contentTypes: []string{"application/ndjson"},
			body:         "{\"id\":1}\n{\"id\":2}\n",
			wantErr:      errUnsanitizableStream,
		},
		{
			name:         "json sequence fails closed",
			path:         "/api/v3/events",
			contentTypes: []string{"application/json-seq"},
			body:         "\x1e{\"id\":1}\n",
			wantErr:      errUnsanitizableStream,
		},
		{
			name:         "stream json fails closed",
			path:         "/api/v3/events",
			contentTypes: []string{"application/stream+json"},
			body:         "{\"id\":1}\n",
			wantErr:      errUnsanitizableStream,
		},
		{
			name:            "encoded ambiguous arr response fails closed",
			path:            "/api/v3/history",
			contentTypes:    []string{"application/octet-stream"},
			contentEncoding: "gzip",
			body:            "not actually compressed",
			wantErr:         errEncodedJSONResponse,
		},
		{
			name:         "mislabelled event stream body fails closed",
			path:         "/api/v3/history",
			contentTypes: []string{"text/plain"},
			body:         "data: {\"apiKey\":\"synthetic\"}\n\n",
			wantErr:      errUnclassifiedArrResponse,
		},
		{
			name:    "missing type event stream body fails closed",
			path:    "/api/v3/history",
			body:    "data: {\"apiKey\":\"synthetic\"}\n\n",
			wantErr: errUnclassifiedArrResponse,
		},
		{
			name:         "xssi prefixed json fails closed",
			path:         "/api/v3/history",
			contentTypes: []string{"text/plain"},
			body:         ")]}'\n{\"apiKey\":\"synthetic\"}",
			wantErr:      errUnclassifiedArrResponse,
		},
		{
			name:         "excessive leading whitespace fails closed into parser",
			path:         "/api/v3/history",
			contentTypes: []string{"text/plain"},
			body:         whitespacePrefix + `{"id":1}`,
			wantSanitize: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(tt.body)),
				Request:    req,
			}
			for _, value := range tt.contentTypes {
				resp.Header.Add("Content-Type", value)
			}
			if tt.contentEncoding != "" {
				resp.Header.Set("Content-Encoding", tt.contentEncoding)
			}

			got, err := shouldSanitizeJSON(resp)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.wantSanitize {
				t.Errorf("sanitize = %v, want %v", got, tt.wantSanitize)
			}
			restored, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				t.Fatalf("read restored body: %v", readErr)
			}
			if string(restored) != tt.body {
				t.Fatal("classification did not preserve the response bytes")
			}
		})
	}
}

// SEC-007: SSE sanitization is selected only for exact v1/v3 arr event routes.
func TestProxyResponseSanitizationModeSelectsOnlyExactArrEventRoutes(t *testing.T) {
	tests := []struct {
		path     string
		wantMode responseSanitizationMode
		wantErr  error
	}{
		{path: "/api/v1/events", wantMode: responseModeSSE},
		{path: "/sonarr/api/v3/events", wantMode: responseModeSSE},
		{path: "/SONARR/API/V3/EVENTS", wantMode: responseModeSSE},
		{path: "/api/v3/history/../events", wantMode: responseModeSSE},
		{path: "/api/v3/events/", wantMode: responseModeOpaque, wantErr: errUnsanitizableStream},
		{path: "/api/v3/events\\", wantMode: responseModeOpaque, wantErr: errUnsanitizableStream},
		{path: "/api/v3/history/events", wantMode: responseModeOpaque, wantErr: errUnsanitizableStream},
		{path: "/api/v3/history/api/v1/events", wantMode: responseModeOpaque, wantErr: errUnsanitizableStream},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader("data: {}\n\n")),
				Request:    httptest.NewRequest(http.MethodGet, tt.path, nil),
			}
			got, err := proxyResponseSanitizationMode(resp)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.wantMode {
				t.Fatalf("mode = %v, want %v", got, tt.wantMode)
			}
		})
	}
}

func TestSanitizeSSEResponseAllowsEmptyStream(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":   []string{"text/event-stream"},
			"Content-Length": []string{"0"},
			"ETag":           []string{`"empty-upstream"`},
		},
		Body:    io.NopCloser(strings.NewReader("")),
		Request: httptest.NewRequest(http.MethodGet, "/api/v3/events", nil),
	}

	if err := sanitizeProxyResponse(resp); err != nil {
		t.Fatalf("sanitize empty SSE: %v", err)
	}
	if resp.Body != http.NoBody || resp.ContentLength != 0 || resp.Header.Get("Content-Length") != "0" {
		t.Fatalf("empty SSE body=%T length=%d header=%q", resp.Body, resp.ContentLength, resp.Header.Get("Content-Length"))
	}
	if got := resp.Header.Get("ETag"); got != "" {
		t.Fatalf("empty rewritten SSE retained ETag %q", got)
	}
}

func TestSanitizedSSEReadCloserDoesNotEmitMalformedLaterEvent(t *testing.T) {
	const laterSecret = "synthetic-later-sse-secret"
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"safe\":\"first\"}\n\n" +
				"data: {\"apiKey\":\"" + laterSecret + "\"}",
		)),
		Request: httptest.NewRequest(http.MethodGet, "/api/v3/events", nil),
	}

	if err := sanitizeProxyResponse(resp); err != nil {
		t.Fatalf("sanitize first SSE event: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if !errors.Is(err, errMalformedSSEResponse) {
		t.Fatalf("read error = %v, want %v", err, errMalformedSSEResponse)
	}
	if bytes.Contains(body, []byte(laterSecret)) {
		t.Fatalf("malformed later event was emitted: %q", body)
	}
	if !bytes.Contains(body, []byte(`"safe":"first"`)) {
		t.Fatalf("valid first event was lost: %q", body)
	}
}

func TestSensitiveNameClassification(t *testing.T) {
	tests := []struct {
		name       string
		objectWant bool
		queryWant  bool
	}{
		{"apiKey", true, true},
		{"prowlarr_api_key", true, true},
		{"password", true, true},
		{"proxyPassphrase", true, true},
		{"secretAccessKey", true, true},
		{"AWS_SECRET_ACCESS_KEY", true, true},
		{"Authorization", true, true},
		{"authorizationHeader", true, true},
		{"apiKeyValue", true, true},
		{"passwordValue", true, true},
		{"tokenValue", true, true},
		{"cookieValue", true, true},
		{"credentialsJson", true, true},
		{"Cookie2", true, true},
		{"Set-Cookie2", true, true},
		{"DPoP", true, true},
		{"CF-Access-Jwt-Assertion", true, true},
		{"X-Goog-IAP-JWT-Assertion", true, true},
		{"nextPageToken", true, true},
		{"key", false, true},
		{"authKey", false, true},
		{"AWSAccessKeyId", false, true},
		{"X-Amz-Credential", true, true},
		{"X-Amz-Signature", false, true},
		{"X-Goog-Signature", false, true},
		{"signature", false, true},
		{"sig", false, true},
		{"ordinaryKey", false, false},
		{"apiKeyConfigured", false, false},
		{"passwordProtected", false, false},
		{"tokenType", false, false},
		{"monkey", false, false},
		{"title", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSensitiveObjectName(tt.name); got != tt.objectWant {
				t.Errorf("isSensitiveObjectName(%q) = %v, want %v", tt.name, got, tt.objectWant)
			}
			if got := isSensitiveQueryName(tt.name); got != tt.queryWant {
				t.Errorf("isSensitiveQueryName(%q) = %v, want %v", tt.name, got, tt.queryWant)
			}
		})
	}
}

func TestSanitizeJSONEnforcesOutputLimitWithoutHTMLEscaping(t *testing.T) {
	t.Run("ordinary HTML punctuation does not expand", func(t *testing.T) {
		body := []byte(`{"safe":"<tag>&value"}`)
		got, err := sanitizeJSONWithOutputLimit(body, int64(len(body)))
		if err != nil {
			t.Fatalf("sanitize JSON: %v", err)
		}
		if !bytes.Contains(got, []byte("<tag>&value")) || bytes.Contains(got, []byte(`\u003c`)) {
			t.Fatalf("JSON punctuation was unexpectedly expanded: %s", got)
		}
	})

	t.Run("redaction expansion is rejected", func(t *testing.T) {
		body := []byte(`[{"token":""},{"token":""},{"token":""}]`)
		if _, err := sanitizeJSONWithOutputLimit(body, int64(len(body))); !errors.Is(err, errJSONResponseTooLarge) {
			t.Fatalf("error = %v, want %v", err, errJSONResponseTooLarge)
		}
	})
}

// INST-018: Recursive proxy scrubbing covers nested credentials and signed URLs.
func TestSanitizeJSONExpandedCredentialCoverage(t *testing.T) {
	const (
		accessSecret = "synthetic-access-secret"
		passphrase   = "synthetic-passphrase"
		bareKey      = "synthetic-bare-key"
		signature    = "synthetic-signature"
		credential   = "synthetic-credential"
		username     = "synthetic-user"
		password     = "synthetic-password"
	)
	body := []byte(`{
		"key":"ordinary object value",
		"secretAccessKey":"` + accessSecret + `",
		"passphrase":"` + passphrase + `",
		"apiKeyValue":"` + accessSecret + `",
		"authorizationHeader":"Bearer ` + accessSecret + `",
		"cookieValue":"session=` + accessSecret + `",
		"credentialsJson":"` + accessSecret + `",
		"downloadUrl":"https://` + username + `:` + password + `@download.invalid/file?safe=kept&key=` + bareKey + `&X-Amz-Signature=` + signature + `&X-Amz-Credential=` + credential + `"
	}`)

	sanitized, err := sanitizeJSON(body)
	if err != nil {
		t.Fatalf("sanitizeJSON: %v", err)
	}
	for _, secret := range []string{accessSecret, passphrase, bareKey, signature, credential, username, password} {
		if bytes.Contains(sanitized, []byte(secret)) {
			t.Fatal("sanitized JSON retained a synthetic credential")
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal(sanitized, &decoded); err != nil {
		t.Fatalf("decode sanitized JSON: %v", err)
	}
	if decoded["key"] != "ordinary object value" {
		t.Errorf("ordinary object key was over-redacted: %v", decoded["key"])
	}
	if decoded["secretAccessKey"] != redactedValue || decoded["passphrase"] != redactedValue {
		t.Error("expanded sensitive object keys were not redacted")
	}
	for _, key := range []string{"apiKeyValue", "authorizationHeader", "cookieValue", "credentialsJson"} {
		if decoded[key] != redactedValue {
			t.Errorf("sensitive prefixed object key %q was not redacted: %v", key, decoded[key])
		}
	}
	if got := decoded["downloadUrl"].(string); !strings.Contains(got, "https://download.invalid/") || !strings.Contains(got, "safe=kept") || strings.Count(got, "REDACTED") != 3 {
		t.Errorf("signed URL was not selectively redacted: %q", got)
	}
}

// INST-018: browser-compatible, decorated, and nested credential URLs are scrubbed.
func TestSanitizeURLCredentialsAdversarialVariants(t *testing.T) {
	const (
		username = "synthetic-url-user"
		password = "synthetic-url-password"
		secret   = "synthetic-url-secret"
	)
	tests := []struct {
		name string
		raw  string
		keep []string
	}{
		{
			name: "mixed special-scheme slashes",
			raw:  "https:/\\" + username + ":" + password + "@download.invalid\\file?safe=kept&token=" + secret,
			keep: []string{"https://download.invalid/file", "safe=kept"},
		},
		{
			name: "protocol-relative backslashes",
			raw:  "\\\\" + username + ":" + password + "@download.invalid\\file?apiKey=" + secret,
			keep: []string{"//download.invalid/file"},
		},
		{
			name: "repeatedly encoded query key",
			raw:  "https://download.invalid/file?API%254bEY=" + secret + "&safe=kept",
			keep: []string{"https://download.invalid/file", "safe=kept"},
		},
		{
			name: "semicolon query separator",
			raw:  "https://download.invalid/file?safe=kept;token=" + secret,
			keep: []string{"https://download.invalid/file", "safe=kept"},
		},
		{
			name: "credential fragment",
			raw:  "https://download.invalid/file#access_token=" + secret + "&safe=kept",
			keep: []string{"https://download.invalid/file", "safe=kept"},
		},
		{
			name: "malformed encoded key",
			raw:  "https://download.invalid/file?safe=kept&bad%ZZ=" + secret,
			keep: []string{"https://download.invalid/file", "safe=kept"},
		},
		{
			name: "WHATWG whitespace normalization",
			raw:  " \th\tttps://" + username + ":" + password + "@download.invalid/file?safe=kept&token=" + secret + "\r\n",
			keep: []string{"https://download.invalid/file", "safe=kept"},
		},
		{
			name: "URL decorated by prose",
			raw:  "download failed: https://" + username + ":" + password + "@download.invalid/file?safe=kept&token=" + secret + " (retry later)",
			keep: []string{"download failed: ", "https://download.invalid/file", "safe=kept", "retry later"},
		},
		{
			name: "URL decorated by invalid scheme prefix",
			raw:  "error 2https://" + username + ":" + password + "@download.invalid/file?safe=kept&token=" + secret,
			keep: []string{"error 2https://download.invalid/file", "safe=kept"},
		},
		{
			name: "multiple URLs separated by comma without space",
			raw:  "mirrors: https://safe.invalid/path,https://" + username + ":" + password + "@download.invalid/file?token=" + secret,
			keep: []string{"mirrors: https://safe.invalid/path", "https://download.invalid/file"},
		},
		{
			name: "multiple URLs separated by semicolon without space",
			raw:  "mirrors: https://safe.invalid/path;https://" + username + ":" + password + "@download.invalid/file?token=" + secret,
			keep: []string{"mirrors: https://safe.invalid/path", "https://download.invalid/file"},
		},
		{
			name: "multiple URLs separated by non-breaking space",
			raw:  "mirrors: https://safe.invalid/path\u00a0https://" + username + ":" + password + "@download.invalid/file?token=" + secret,
			keep: []string{"mirrors: https://safe.invalid/path", "https://download.invalid/file"},
		},
		{
			name: "decorated protocol-relative URL",
			raw:  "mirror: //" + username + ":" + password + "@download.invalid/file?token=" + secret,
			keep: []string{"mirror: //download.invalid/file"},
		},
		{
			name: "second protocol-relative URL without space",
			raw:  "mirrors: https://safe.invalid/path,//" + username + ":" + password + "@download.invalid/file?token=" + secret,
			keep: []string{"mirrors: https://safe.invalid/path", "//download.invalid/file"},
		},
		{
			name: "encoded nested redirect URL",
			raw:  "https://safe.invalid/go?redirect=https%3A%2F%2F" + username + "%3A" + password + "%40download.invalid%2Ffile%3FapiKey%3D" + secret + "&safe=kept",
			keep: []string{"https://safe.invalid/go", "safe=kept"},
		},
		{
			name: "double encoded nested redirect URL",
			raw:  "https://safe.invalid/go?redirect=https%253A%252F%252F" + username + "%253A" + password + "%2540download.invalid%252Ffile%253Ftoken%253D" + secret + "&safe=kept",
			keep: []string{"https://safe.invalid/go", "safe=kept"},
		},
		{
			name: "encoded redirect containing multiple URLs",
			raw: "https://safe.invalid/go?redirect=https%3A%2F%2Fmirror.invalid%2Fpath%2Chttps%3A%2F%2F" + username +
				"%3A" + password + "%40download.invalid%2Ffile%3Ftoken%3D" + secret + "&safe=kept",
			keep: []string{"https://safe.invalid/go", "safe=kept"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeURLCredentials(tt.raw)
			for _, credential := range []string{username, password, secret} {
				if strings.Contains(got, credential) {
					t.Fatalf("sanitized URL retained %q: %q", credential, got)
				}
			}
			for _, safe := range tt.keep {
				if !strings.Contains(got, safe) {
					t.Errorf("sanitized URL lost %q: %q", safe, got)
				}
			}
		})
	}
}

func TestCredentialURLDecodingDepthFailsClosed(t *testing.T) {
	// This spelling removes only one %25 layer per QueryUnescape call. It used to
	// make the sanitizer rescan an attacker-sized value once per layer.
	deepEncoding := "ordinary%" + strings.Repeat("25", maxCredentialDecodePasses+64)
	if !queryKeyIsSensitive(deepEncoding) {
		t.Fatal("deeper-than-supported query-key encoding did not fail closed")
	}
	if !parameterValueContainsCredentials(deepEncoding) {
		t.Fatal("deeper-than-supported parameter-value encoding did not fail closed")
	}
}

func TestSanitizeManyEmbeddedURLReferences(t *testing.T) {
	const (
		username = "many-url-user-secret"
		password = "many-url-password-secret"
		token    = "many-url-token-secret"
	)
	raw := "mirrors: " + strings.Repeat("https://safe.invalid/path,", 8192) +
		"https://" + username + ":" + password + "@download.invalid/file?token=" + token
	got := sanitizeURLCredentials(raw)
	for _, secret := range []string{username, password, token} {
		if strings.Contains(got, secret) {
			t.Fatalf("many-URL sanitizer retained %q", secret)
		}
	}
	if !strings.HasSuffix(got, "https://download.invalid/file?token=%5BREDACTED%5D") {
		t.Fatalf("many-URL sanitizer lost final destination: suffix=%q", got[len(got)-min(len(got), 128):])
	}
}

// INST-018: Secret-bearing response URL headers are scrubbed without losing routing.
func TestSanitizeResponseURLHeaders(t *testing.T) {
	const (
		username = "header-user-secret"
		password = "header-password-secret"
		token    = "header-token-secret"
	)
	header := make(http.Header)
	header.Add("Location", "https://"+username+":"+password+"@arr.invalid/next?safe=1&token="+token)
	header.Add("Content-Location", "/history?page=2&apiKey="+token)
	header.Add("Link", `<https://`+username+`:`+password+`@arr.invalid/page?safe=1&signature=`+token+`>; rel="next"; token="`+token+`", </page?safe=2&token=`+token+`>; rel="last"; anchor="https://`+username+`:`+password+`@anchor.invalid/?token=`+token+`"`)
	header.Add("Refresh", `5; URL="https://`+username+`:`+password+`@arr.invalid/login?authKey=`+token+`"`)
	header.Add("X-Download-URL", "https:/\\"+username+":"+password+"@downloads.invalid\\file?safe=3&API%254bEY="+token)
	header.Add("X-Callback-URL", "https://callbacks.invalid/ready?safe=4;token="+token)
	header.Add("X-Error-Detail", "download failed: https://"+username+":"+password+"@errors.invalid/file?safe=5&token="+token+" (retry)")
	header.Add("Set-Cookie2", "session="+token)

	sanitizeResponseHeaders(header)

	for name, values := range header {
		for _, value := range values {
			for _, secret := range []string{username, password, token} {
				if strings.Contains(value, secret) {
					t.Errorf("%s retained a synthetic credential: %q", name, value)
				}
			}
		}
	}
	if got := header.Get("Location"); !strings.Contains(got, "https://arr.invalid/next?safe=1") {
		t.Errorf("Location destination was not preserved: %q", got)
	}
	if got := header.Get("Content-Location"); !strings.Contains(got, "/history?page=2") {
		t.Errorf("Content-Location destination was not preserved: %q", got)
	}
	if got := header.Get("Link"); strings.Count(got, "rel=") != 2 || !strings.Contains(got, "safe=1") || !strings.Contains(got, "safe=2") {
		t.Errorf("Link relationships were not preserved: %q", got)
	}
	if got := header.Get("Refresh"); !strings.HasPrefix(got, `5; url="https://arr.invalid/login?`) {
		t.Errorf("Refresh behavior was not preserved: %q", got)
	}
	if got := header.Get("X-Download-URL"); !strings.Contains(got, "https://downloads.invalid/file?safe=3") {
		t.Errorf("extension download URL routing was not preserved: %q", got)
	}
	if got := header.Get("X-Callback-URL"); !strings.Contains(got, "https://callbacks.invalid/ready?safe=4") {
		t.Errorf("extension callback URL routing was not preserved: %q", got)
	}
	if got := header.Get("X-Error-Detail"); !strings.Contains(got, "download failed: https://errors.invalid/file?safe=5") {
		t.Errorf("decorated extension URL text was not preserved safely: %q", got)
	}
	if got := header.Get("Set-Cookie2"); got != "" {
		t.Errorf("legacy credential response header survived: %q", got)
	}
}

// INST-018: Malformed secret-bearing URL headers fail closed.
func TestSanitizeResponseDropsMalformedURLHeaders(t *testing.T) {
	for _, link := range []string{
		`<https://user:password@arr.invalid/page`,
		`token=synthetic-prefix-secret; <https://safe.invalid/page>; rel="next"`,
		`<https://safe.invalid/page>; rel="next" trailing`,
	} {
		t.Run(link, func(t *testing.T) {
			header := make(http.Header)
			header.Set("Link", link)
			header.Set("Refresh", `soon; url=https://user:password@arr.invalid/page`)
			sanitizeResponseHeaders(header)
			if got := header.Get("Link"); got != "" {
				t.Errorf("malformed Link was retained: %q", got)
			}
			if got := header.Get("Refresh"); got != "" {
				t.Errorf("malformed Refresh was retained: %q", got)
			}
		})
	}
}

func TestSanitizeLinkDropsArbitraryParameterValues(t *testing.T) {
	const secret = "synthetic-link-parameter-secret"
	header := make(http.Header)
	header.Set("Link", `<https://assets.invalid/app.js>; rel="preload"; as="script"; crossorigin; fetchpriority="high"; referrerpolicy="no-referrer"; integrity="`+secret+`"; media="`+secret+`"; type="`+secret+`"; hreflang="`+secret+`"; rev="`+secret+`"; extension="`+secret+`"; rel="https://user:`+secret+`@relation.invalid/?token=`+secret+`"`)

	sanitizeResponseHeaders(header)

	got := header.Get("Link")
	if strings.Contains(got, secret) || strings.Contains(got, "integrity=") || strings.Contains(got, "extension=") {
		t.Fatalf("Link retained arbitrary parameter text: %q", got)
	}
	for _, safe := range []string{`rel="preload"`, `as="script"`, "crossorigin", `fetchpriority="high"`, `referrerpolicy="no-referrer"`} {
		if !strings.Contains(got, safe) {
			t.Errorf("Link lost finite safe parameter %q: %q", safe, got)
		}
	}
}

func TestArrAPIPathSuffix(t *testing.T) {
	tests := []struct {
		path string
		want string
		ok   bool
	}{
		{"/api/v3/history", "/api/v3/history", true},
		{"/sonarr/api/v3/history", "/api/v3/history", true},
		{"/SONARR/API/V3/HISTORY", "/API/V3/HISTORY", true},
		{"/nested/base/api/v1/author", "/api/v1/author", true},
		{"/api/v3/history/api/v1/events", "/api/v3/history/api/v1/events", true},
		{"/api/v3/MediaCover/../history", "/api/v3/history", true},
		{"/sonarr/api/v3/log/file/../../history", "/api/v3/history", true},
		{"/sonarr\\API\\V3\\HISTORY", "/API/V3/HISTORY", true},
		{"/sonarr//API//V3//HISTORY", "/API/V3/HISTORY", true},
		{"/api/v3/events\\", "/api/v3/events/", true},
		{"/sonarr/api/v30/history", "", false},
		{"/sonarr/not-api/v3/history", "", false},
	}
	for _, tt := range tests {
		got, ok := arrAPIPathSuffix(tt.path)
		if got != tt.want || ok != tt.ok {
			t.Errorf("arrAPIPathSuffix(%q) = %q, %v; want %q, %v", tt.path, got, ok, tt.want, tt.ok)
		}
	}
}
