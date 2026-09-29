package musicdiscovery

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

var artworkPNG = []byte{137, 80, 78, 71, 13, 10, 26, 10}

func artworkResponse(status int, body io.Reader) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(body)}
}

type failedArtworkBody struct{ err error }

func (b failedArtworkBody) Read([]byte) (int, error) { return 0, b.err }

func TestArtworkRecoversTransientFailureAndCachesResult(t *testing.T) {
	for _, tc := range []struct {
		name  string
		first roundTripFunc
		delay time.Duration
	}{
		{"connection reset", func(*http.Request) (*http.Response, error) {
			return nil, &net.OpError{Op: "read", Err: syscall.ECONNRESET}
		}, time.Second},
		{"interrupted body", func(*http.Request) (*http.Response, error) {
			return artworkResponse(200, failedArtworkBody{io.ErrUnexpectedEOF}), nil
		}, time.Second},
		{"unavailable", func(*http.Request) (*http.Response, error) {
			return artworkResponse(503, strings.NewReader("unavailable")), nil
		}, time.Second},
		{"rate limited", func(*http.Request) (*http.Response, error) {
			response := artworkResponse(429, strings.NewReader("rate limited"))
			response.Header.Set("Retry-After", "3")
			return response, nil
		}, 3 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := NewService()
				hits := 0
				s.art.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					hits++
					if hits == 1 {
						return tc.first(r)
					}
					return artworkResponse(200, bytes.NewReader(artworkPNG)), nil
				})
				started := time.Now()
				for range 2 {
					body, err := s.Artwork(context.Background(), aID)
					if err != nil || !bytes.Equal(body, artworkPNG) {
						t.Fatalf("artwork = %x, %v", body, err)
					}
				}
				if hits != 2 || time.Since(started) != tc.delay {
					t.Fatalf("requests = %d, elapsed = %v; want one retry after %v and a cache hit", hits, time.Since(started), tc.delay)
				}
			})
		})
	}
}

func TestArtworkDoesNotRetryPermanentFailuresOrLongRateLimit(t *testing.T) {
	for _, tc := range []struct {
		name string
		trip roundTripFunc
	}{
		{"TLS verification", func(*http.Request) (*http.Response, error) {
			return nil, &tls.CertificateVerificationError{Err: errors.New("private certificate details")}
		}},
		{"canceled transport", func(*http.Request) (*http.Response, error) {
			return nil, context.Canceled
		}},
		{"canceled body", func(*http.Request) (*http.Response, error) {
			return artworkResponse(200, failedArtworkBody{context.Canceled}), nil
		}},
		{"forbidden", func(*http.Request) (*http.Response, error) {
			return artworkResponse(403, strings.NewReader("forbidden")), nil
		}},
		{"invalid raster", func(*http.Request) (*http.Response, error) {
			return artworkResponse(200, strings.NewReader("<svg/>")), nil
		}},
		{"oversized raster", func(*http.Request) (*http.Response, error) {
			return artworkResponse(200, bytes.NewReader(append(artworkPNG, make([]byte, 2<<20)...))), nil
		}},
		{"long Retry-After", func(*http.Request) (*http.Response, error) {
			response := artworkResponse(429, strings.NewReader("rate limited"))
			response.Header.Set("Retry-After", "60")
			return response, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := NewService()
				hits := 0
				s.art.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					hits++
					return tc.trip(r)
				})
				started := time.Now()
				if _, err := s.Artwork(context.Background(), aID); err == nil || hits != 1 || time.Since(started) != 0 {
					t.Fatalf("error = %v, requests = %d, elapsed = %v", err, hits, time.Since(started))
				}
			})
		})
	}
}

func TestArtworkRetrySharesOriginalDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := NewService()
		hits := 0
		s.art.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			hits++
			if err := pause(r.Context(), 8*time.Second); err != nil {
				return nil, err
			}
			return nil, syscall.ECONNRESET
		})
		started := time.Now()
		_, err := s.Artwork(context.Background(), aID)
		if !errors.Is(err, context.DeadlineExceeded) || hits != 2 || time.Since(started) != 15*time.Second {
			t.Fatalf("error = %v, requests = %d, elapsed = %v; want a single 15-second budget", err, hits, time.Since(started))
		}
	})
}

func TestArtworkCancellationStopsRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := NewService()
		hits := 0
		s.art.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			hits++
			return nil, syscall.ECONNRESET
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { _, err := s.Artwork(ctx, aID); done <- err }()
		synctest.Wait() // The first attempt has failed and is waiting to retry.
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		synctest.Wait() // Wait for the shared fill to observe its last caller leaving.
		if hits != 1 {
			t.Fatalf("canceled artwork was retried: %d requests", hits)
		}
	})
}

func TestArtworkExhaustedRetryReportsSafeReason(t *testing.T) {
	var logs bytes.Buffer
	oldWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldWriter) })
	synctest.Test(t, func(t *testing.T) {
		s := NewService()
		hits := 0
		s.art.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			hits++
			return nil, &url.Error{Op: "Get", URL: "https://private-host/?token=secret-token", Err: syscall.ECONNRESET}
		})
		_, err := s.Artwork(context.Background(), aID)
		if err == nil || hits != 2 || !strings.Contains(err.Error(), "connection reset") {
			t.Fatalf("error = %v, requests = %d", err, hits)
		}
		if !strings.Contains(logs.String(), "music artwork: music provider is unavailable: connection reset") {
			t.Fatalf("missing classified failure: %s", &logs)
		}
		for _, private := range []string{"private-host", "secret-token", "https://", aID} {
			if strings.Contains(err.Error()+logs.String(), private) {
				t.Fatalf("artwork failure disclosed %q", private)
			}
		}
	})
}
