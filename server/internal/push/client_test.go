package push

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// capturedRequest records what a mock gateway received.
type capturedRequest struct {
	method string
	path   string
	auth   string
	body   map[string]any
}

// newMockGateway returns an httptest server that records the last request into
// got and replies with the given status + body. It NEVER calls the real
// gateway.
func newMockGateway(t *testing.T, status int, respBody string, got *capturedRequest) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		got.auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		got.body = map[string]any{}
		_ = json.Unmarshal(raw, &got.body)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClientRegisterDevice(t *testing.T) {
	var got capturedRequest
	srv := newMockGateway(t, http.StatusOK, `{"id":"d1","created":true}`, &got)

	c := NewClient(srv.URL, "pgk_test")
	if err := c.RegisterDevice(context.Background(), 42, "device-1", "ios", "abc123"); err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}

	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if got.path != "/v1/devices" {
		t.Errorf("path = %q, want /v1/devices", got.path)
	}
	if got.auth != "Bearer pgk_test" {
		t.Errorf("auth = %q, want Bearer pgk_test", got.auth)
	}
	// int64 user id must be sent as a string.
	if got.body["user_id"] != "42" {
		t.Errorf("user_id = %v, want \"42\"", got.body["user_id"])
	}
	if got.body["device_id"] != "device-1" || got.body["platform"] != "ios" || got.body["token"] != "abc123" {
		t.Errorf("unexpected body: %v", got.body)
	}
	// The gateway defaults the topic; we must not send one.
	if _, ok := got.body["topic"]; ok {
		t.Errorf("body should not include a topic: %v", got.body)
	}
}

func TestClientDeleteDevice(t *testing.T) {
	var got capturedRequest
	srv := newMockGateway(t, http.StatusNoContent, "", &got)

	c := NewClient(srv.URL, "pgk_test")
	if err := c.DeleteDevice(context.Background(), "device-1"); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}

	if got.method != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", got.method)
	}
	if got.path != "/v1/devices" {
		t.Errorf("path = %q, want /v1/devices", got.path)
	}
	if got.auth != "Bearer pgk_test" {
		t.Errorf("auth = %q, want Bearer pgk_test", got.auth)
	}
	if got.body["device_id"] != "device-1" {
		t.Errorf("device_id = %v, want device-1", got.body["device_id"])
	}
}

func TestClientSend(t *testing.T) {
	var got capturedRequest
	srv := newMockGateway(t, http.StatusOK, `{"sent":1,"failed":0}`, &got)

	c := NewClient(srv.URL, "pgk_test")
	err := c.Send(context.Background(), []int64{7, 9}, "Title", "Body", map[string]any{"type": "request_decision"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got.method != http.MethodPost || got.path != "/v1/notifications" {
		t.Errorf("method/path = %q %q, want POST /v1/notifications", got.method, got.path)
	}
	to, _ := got.body["to"].(map[string]any)
	ids, _ := to["user_ids"].([]any)
	if len(ids) != 2 || ids[0] != "7" || ids[1] != "9" {
		t.Errorf("user_ids = %v, want [\"7\" \"9\"]", ids)
	}
	notif, _ := got.body["notification"].(map[string]any)
	if notif["title"] != "Title" || notif["body"] != "Body" {
		t.Errorf("notification = %v, want title/body Title/Body", notif)
	}
	opts, _ := got.body["options"].(map[string]any)
	if opts["priority"] != "high" {
		t.Errorf("priority = %v, want high", opts["priority"])
	}
}

func TestClientSendNon2xxIsError(t *testing.T) {
	var got capturedRequest
	srv := newMockGateway(t, http.StatusUnauthorized, `{"error":{"code":"unauthorized"}}`, &got)

	c := NewClient(srv.URL, "pgk_bad")
	err := c.Send(context.Background(), []int64{1}, "t", "b", nil)
	if err == nil {
		t.Fatal("expected error for non-2xx response, got nil")
	}
}
