package transporterr

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"syscall"
	"testing"
)

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestSummarize(t *testing.T) {
	dnsErr := &net.DNSError{Err: "no such host", Name: "radarr"}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"dns failure", &url.Error{Op: "Get", URL: "http://radarr:7878/api", Err: &net.OpError{Op: "dial", Err: dnsErr}}, "could not resolve host"},
		{"connection refused", &url.Error{Op: "Get", URL: "http://radarr:7878/api", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}, "connection refused"},
		{"timeout", &url.Error{Op: "Get", URL: "http://radarr:7878/api", Err: timeoutError{}}, "request timed out"},
		{"context deadline", &url.Error{Op: "Get", URL: "http://radarr:7878/api", Err: context.DeadlineExceeded}, "request timed out"},
		{"other", errors.New("http: server closed idle connection"), "could not connect"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Summarize(tc.err)
			if got != tc.want {
				t.Fatalf("Summarize = %q, want %q", got, tc.want)
			}
			// The whole point: no host, port, or URL fragments survive.
			for _, leak := range []string{"radarr", "7878", "http://"} {
				if strings.Contains(got, leak) {
					t.Fatalf("Summarize leaked %q in %q", leak, got)
				}
			}
		})
	}
}
