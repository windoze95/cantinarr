// Package transporterr summarizes HTTP transport failures without naming the
// dialed host. Errors from http.Client.Do embed the full request URL — and
// DNS failures repeat the hostname — so returning them verbatim from service
// clients leaks internal topology (e.g. http://radarr:7878) into whatever
// wraps the error, including requester-facing request failures.
package transporterr

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"syscall"
)

// Summarize classifies err into a short, host-free description of why the
// request never produced a response.
func Summarize(err error) string {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "could not resolve host"
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return "TLS certificate verification failed"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "request timed out"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "request timed out"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "connection refused"
	}
	return "could not connect"
}
