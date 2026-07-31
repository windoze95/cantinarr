package arr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/secrets"
)

func TestDoSettingsWriteSendsRawJSONAndReturnsRawResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v3/customformat" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Api-Key") != "secret-key" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("headers = %#v", r.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["name"] != "x265" {
			t.Errorf("body = %#v, err = %v", body, err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":9,"name":"x265"}`))
	}))
	t.Cleanup(server.Close)

	raw, status, err := DoSettingsWrite(context.Background(), server.Client(), "radarr", server.URL, "secret-key", http.MethodPost, "/api/v3/customformat", json.RawMessage(`{"name":"x265"}`))
	if err != nil || status != http.StatusCreated || string(raw) != `{"id":9,"name":"x265"}` {
		t.Fatalf("response = %s status=%d err=%v", raw, status, err)
	}
}

func TestDoSettingsWriteProjectsOnlyRedactedValidationDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`[{
			"propertyName":"X-Api-Key: property-secret cutoff",
			"errorMessage":"request https://indexer.invalid/a?token=message-secret&item=7 failed",
			"attemptedValue":"https://indexer.invalid/download?apiKey=ignored-secret"
		}]`))
	}))
	t.Cleanup(server.Close)

	_, status, err := DoSettingsWrite(context.Background(), server.Client(), "sonarr", server.URL, "key", http.MethodPut, "/api/v3/customformat/4", json.RawMessage(`{}`))
	if status != http.StatusBadRequest || err == nil {
		t.Fatalf("status=%d err=%v", status, err)
	}
	text := err.Error()
	for _, leaked := range []string{"property-secret", "message-secret", "ignored-secret"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("error leaked %q: %s", leaked, text)
		}
	}
	if !strings.Contains(text, secrets.RedactedValue) || !strings.Contains(text, "cutoff") || !strings.Contains(text, "item=7") {
		t.Fatalf("error lost safe validation detail: %s", text)
	}
}

func TestDoSettingsWriteRemovesKnownKeyAndURLAuthoritiesFromValidation(t *testing.T) {
	const apiKey = "0123456789abcdef0123456789abcdef"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `[{"propertyName":"url","errorMessage":"failed at %s/private and https://indexer.internal:9696/check using %s"}]`, server.URL, apiKey)
	}))
	t.Cleanup(server.Close)

	_, _, err := DoSettingsWrite(context.Background(), server.Client(), "radarr", server.URL, apiKey, http.MethodPost, "/api/v3/customformat", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected validation error")
	}
	for _, leaked := range []string{apiKey, strings.TrimPrefix(server.URL, "http://"), "indexer.internal", "9696"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("validation error leaked %q: %s", leaked, err)
		}
	}
	if !strings.Contains(err.Error(), secrets.RedactedValue) || !strings.Contains(err.Error(), "/private") {
		t.Fatalf("validation error lost useful redacted context: %s", err)
	}
}

func TestDoSettingsWriteDiscardsUntrustedErrorBodies(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "non validation status", status: http.StatusInternalServerError, body: `server-secret signed-url-secret`},
		{name: "malformed validation", status: http.StatusBadRequest, body: `[{"propertyName":"cutoff","errorMessage":"body-secret"}`},
		{name: "non array validation", status: http.StatusBadRequest, body: `{"errorMessage":"body-secret"}`},
		{name: "trailing validation", status: http.StatusBadRequest, body: `[{"propertyName":"cutoff","errorMessage":"body-secret"}] {}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(server.Close)

			_, status, err := DoSettingsWrite(context.Background(), server.Client(), "radarr", server.URL, "key", http.MethodPost, "/api/v3/customformat", json.RawMessage(`{}`))
			if status != tt.status || err == nil {
				t.Fatalf("status=%d err=%v", status, err)
			}
			for _, leaked := range []string{"server-secret", "signed-url-secret", "body-secret"} {
				if strings.Contains(err.Error(), leaked) {
					t.Fatalf("error leaked %q: %s", leaked, err)
				}
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("returned status %d", tt.status)) {
				t.Fatalf("error lost status: %s", err)
			}
		})
	}
}

func TestDoSettingsWriteClassifiesServerErrorAsOutcomeUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`secret response body`))
	}))
	t.Cleanup(server.Close)

	_, status, err := DoSettingsWrite(context.Background(), server.Client(), "radarr", server.URL, "key", http.MethodPost, "/api/v3/customformat", json.RawMessage(`{}`))
	var unknown *SettingsWriteOutcomeUnknownError
	if status != http.StatusInternalServerError || !errors.As(err, &unknown) || !strings.Contains(err.Error(), "may already have been applied") {
		t.Fatalf("status=%d err=%T %v", status, err, err)
	}
	if strings.Contains(err.Error(), "secret response body") {
		t.Fatalf("server error body leaked: %v", err)
	}
}

func TestDoSettingsWriteClassifiesUncertainResponsePaths(t *testing.T) {
	assertUnknown := func(t *testing.T, err error) {
		t.Helper()
		var unknown *SettingsWriteOutcomeUnknownError
		if !errors.As(err, &unknown) || !strings.Contains(err.Error(), "inspect the live settings before retrying") {
			t.Fatalf("error = %T %v, want outcome unknown", err, err)
		}
	}

	t.Run("transport error", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial failed")
		})}
		_, _, err := DoSettingsWrite(context.Background(), client, "radarr", "http://arr.internal:7878", "key", http.MethodPost, "/api/v3/customformat", json.RawMessage(`{}`))
		assertUnknown(t, err)
		if strings.Contains(err.Error(), "arr.internal") || strings.Contains(err.Error(), "7878") {
			t.Fatalf("transport error leaked host: %v", err)
		}
	})

	t.Run("response read error", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: failingReadCloser{}}, nil
		})}
		_, _, err := DoSettingsWrite(context.Background(), client, "radarr", "http://arr.internal:7878", "key", http.MethodPost, "/api/v3/customformat", json.RawMessage(`{}`))
		assertUnknown(t, err)
	})

	t.Run("oversize response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, strings.Repeat("x", maxSettingsWriteResponseBytes+1))
		}))
		t.Cleanup(server.Close)
		_, _, err := DoSettingsWrite(context.Background(), server.Client(), "radarr", server.URL, "key", http.MethodPost, "/api/v3/customformat", json.RawMessage(`{}`))
		assertUnknown(t, err)
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (failingReadCloser) Close() error             { return nil }
