package api

import (
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	serverRequestMaxBytes   = 32 << 20
	serverRequestReadWindow = 60 * time.Second
	publicRequestMaxBytes   = 1 << 20
	publicRequestReadWindow = 30 * time.Second
)

// publicRequestBounds covers the credential and unauthenticated write routes.
// The router's broad limit also covers unauthenticated requests that fail
// authorization before a handler reads their body. Neither deadline limits
// response writes, so WebSocket and MCP streams can remain open.
func publicRequestBounds(next http.Handler) http.Handler {
	return requestBodyBounds(publicRequestMaxBytes, publicRequestReadWindow)(next)
}

func requestBodyBounds(maxBytes int64, readWindow time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/ws" {
				// The WebSocket upgrader hijacks the TCP connection. Any read
				// deadline set here would follow it into the live socket.
				if r.Body != nil && r.Body != http.NoBody {
					w.Header().Set("Connection", "close")
					http.Error(w, "WebSocket requests cannot have a body", http.StatusBadRequest)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			if r.ContentLength > maxBytes {
				// Skip net/http's unread-body drain. A client claiming a huge
				// length could otherwise trickle that drain after this response.
				w.Header().Set("Connection", "close")
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			if r.Body != nil && r.Body != http.NoBody {
				// A slow client can otherwise hold a body read indefinitely even
				// after ReadHeaderTimeout has protected the header phase.
				controller := http.NewResponseController(w)
				err := controller.SetReadDeadline(time.Now().Add(readWindow))
				if err != nil && !errors.Is(err, http.ErrNotSupported) {
					w.Header().Set("Connection", "close")
					http.Error(w, "request unavailable", http.StatusServiceUnavailable)
					return
				}
				bounded := http.MaxBytesReader(w, r.Body, maxBytes)
				if err == nil {
					// MCP POST may keep its response stream open after consuming
					// the JSON request. Clear the read deadline once the body is
					// fully read; otherwise retain it through net/http's drain.
					r.Body = &deadlineBody{ReadCloser: bounded, remaining: r.ContentLength, reset: func() {
						_ = controller.SetReadDeadline(time.Time{})
					}}
				} else {
					r.Body = bounded
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

type deadlineBody struct {
	io.ReadCloser
	remaining int64 // negative for chunked/unknown length
	reset     func()
	once      sync.Once
}

func (b *deadlineBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if b.remaining >= 0 {
		b.remaining -= int64(n)
	}
	if err == io.EOF || (b.remaining == 0 && err == nil) {
		b.once.Do(b.reset)
	}
	return n, err
}
