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
			name:         "event stream remains streaming",
			path:         "/api/v3/events",
			contentTypes: []string{"text/event-stream"},
			body:         "data: {\"id\":1}\n\n",
		},
		{
			name:         "ndjson fails closed",
			path:         "/api/v3/events",
			contentTypes: []string{"application/x-ndjson"},
			body:         "{\"id\":1}\n{\"id\":2}\n",
			wantErr:      errStreamingJSONResponse,
		},
		{
			name:         "json sequence fails closed",
			path:         "/api/v3/events",
			contentTypes: []string{"application/json-seq"},
			body:         "\x1e{\"id\":1}\n",
			wantErr:      errStreamingJSONResponse,
		},
		{
			name:         "stream json fails closed",
			path:         "/api/v3/events",
			contentTypes: []string{"application/stream+json"},
			body:         "{\"id\":1}\n",
			wantErr:      errStreamingJSONResponse,
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
	if got := decoded["downloadUrl"].(string); !strings.Contains(got, "https://download.invalid/") || !strings.Contains(got, "safe=kept") || strings.Count(got, "REDACTED") != 3 {
		t.Errorf("signed URL was not selectively redacted: %q", got)
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
	header.Add("Link", `<https://`+username+`:`+password+`@arr.invalid/page?safe=1&signature=`+token+`>; rel="next", </page?safe=2&token=`+token+`>; rel="last"`)
	header.Add("Refresh", `5; URL="https://`+username+`:`+password+`@arr.invalid/login?authKey=`+token+`"`)

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
}

// INST-018: Malformed secret-bearing URL headers fail closed.
func TestSanitizeResponseDropsMalformedURLHeaders(t *testing.T) {
	header := make(http.Header)
	header.Set("Link", `<https://user:password@arr.invalid/page`)
	header.Set("Refresh", `soon; url=https://user:password@arr.invalid/page`)

	sanitizeResponseHeaders(header)

	if got := header.Get("Link"); got != "" {
		t.Errorf("malformed Link was retained: %q", got)
	}
	if got := header.Get("Refresh"); got != "" {
		t.Errorf("malformed Refresh was retained: %q", got)
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
		{"/nested/base/api/v1/author", "/api/v1/author", true},
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
