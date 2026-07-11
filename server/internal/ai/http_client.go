package ai

import (
	"net/http"
	"time"
)

const providerResponseHeaderTimeout = 10 * time.Minute

// newCredentialHTTPClient builds the transport used by every hosted AI
// provider. Provider keys live in custom headers as well as Authorization, so
// redirects are never followed: even a same-domain redirect must not replay a
// credential-bearing request to a different endpoint.
func newCredentialHTTPClient(timeout time.Duration) *http.Client {
	client := &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	if transport, ok := http.DefaultTransport.(*http.Transport); ok {
		cloned := transport.Clone()
		cloned.ResponseHeaderTimeout = providerResponseHeaderTimeout
		client.Transport = cloned
	} else {
		client.Transport = http.DefaultTransport
	}
	return client
}
