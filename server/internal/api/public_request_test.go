package api

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPublicLoginLimitIgnoresForgedForwardedAddresses(t *testing.T) {
	harness := newRBACRouterHarness(t, false)
	for attempt := 1; attempt <= 11; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader("{"))
		req.RemoteAddr = fmt.Sprintf("198.51.100.10:%d", 20000+attempt)
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", attempt))
		req.Header.Set("X-Real-IP", fmt.Sprintf("192.0.2.%d", attempt))
		recorder := httptest.NewRecorder()
		harness.router.ServeHTTP(recorder, req)
		want := http.StatusBadRequest
		if attempt == 11 {
			want = http.StatusTooManyRequests
		}
		if recorder.Code != want {
			t.Fatalf("attempt %d = %d, want %d", attempt, recorder.Code, want)
		}
	}

	// A different socket peer gets its own budget, even if it claims the
	// same forwarded address as the exhausted peer.
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader("{"))
	req.RemoteAddr = "198.51.100.11:30001"
	req.Header.Set("X-Forwarded-For", "203.0.113.11")
	recorder := httptest.NewRecorder()
	harness.router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("other peer = %d, want 400", recorder.Code)
	}
}

func TestPublicRequestBoundsRejectsKnownAndChunkedOversizeBodies(t *testing.T) {
	harness := newRBACRouterHarness(t, false)
	for _, path := range []string{"/api/auth/login", "/api/auth/refresh", "/mcp"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(strings.Repeat("x", publicRequestMaxBytes+1)))
			recorder := httptest.NewRecorder()
			harness.router.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("oversize status=%d, want 413", recorder.Code)
			}
			if recorder.Header().Get("Connection") != "close" {
				t.Fatal("oversized request must close the connection without draining its body")
			}
		})
	}

	handler := requestBodyBounds(8, time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		if err != nil {
			t.Errorf("read body: %v", err)
		}
	}))
	for _, chunked := range []bool{false, true} {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("123456789"))
		if chunked {
			req.ContentLength = -1
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("chunked=%t status=%d, want 413", chunked, recorder.Code)
		}
	}
}

func TestPublicRequestBoundsStopsSlowBodyRead(t *testing.T) {
	readErr := make(chan error, 1)
	server := httptest.NewServer(requestBodyBounds(8, 100*time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		readErr <- err
	})))
	defer server.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 2\r\n\r\na"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readErr:
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			t.Fatalf("body read = %v, want timeout", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("slow body read remained blocked past its deadline")
	}
}

func TestRequestBodyBoundsLeavesLongLivedMCPResponseOpen(t *testing.T) {
	server := httptest.NewServer(requestBodyBounds(8, 100*time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			t.Errorf("read MCP request: %v", err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "first\n")
		w.(http.Flusher).Flush()
		time.Sleep(250 * time.Millisecond)
		_, _ = io.WriteString(w, "second\n")
	})))
	defer server.Close()
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Post(server.URL+"/mcp", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "first\nsecond\n" {
		t.Fatalf("stream = %q", data)
	}
}

func TestRequestBodyBoundsDoesNotSetDeadlineOnWebSocketUpgrade(t *testing.T) {
	readErr := make(chan error, 1)
	server := httptest.NewServer(requestBodyBounds(8, 100*time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			readErr <- err
			return
		}
		defer conn.Close()
		_, _ = io.WriteString(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		_ = rw.Flush()
		var one [1]byte
		_, err = conn.Read(one[:])
		readErr <- err
	})))
	defer server.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "GET /api/ws HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 128)
	if _, err := conn.Read(buf); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	if _, err := conn.Write([]byte{'x'}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readErr:
		if err != nil {
			t.Fatalf("hijacked connection read after deadline: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hijacked connection stopped responding")
	}
}
