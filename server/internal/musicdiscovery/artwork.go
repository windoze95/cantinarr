package musicdiscovery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/httpx"
	"github.com/windoze95/cantinarr-server/internal/transporterr"
)

const artworkRequestTimeout = 15 * time.Second

var errArtworkRedirect = errors.New("artwork redirect is outside the artwork providers")

func artworkRedirect(req *http.Request, via []*http.Request) error {
	host := strings.ToLower(req.URL.Hostname())
	if len(via) >= 5 || req.URL.Scheme != "https" || req.URL.User != nil ||
		(req.URL.Port() != "" && req.URL.Port() != "443") ||
		!(host == "coverartarchive.org" || host == "archive.org" || strings.HasSuffix(host, ".archive.org")) {
		return errArtworkRedirect
	}
	return nil
}

func newArtworkClient() *http.Client {
	return &http.Client{Transport: httpx.External(), Timeout: artworkRequestTimeout, CheckRedirect: artworkRedirect}
}

// Only a validated release-group ID constructs the origin URL. No upstream
// URL from a feed, metadata field, or caller is ever dereferenced.
func (s *Service) Artwork(ctx context.Context, id string) ([]byte, error) {
	if !validID(id) {
		return nil, errors.New("invalid MusicBrainz release-group ID")
	}
	return s.cache.get(ctx, "art:"+id, 24*time.Hour, func(ctx context.Context) (body []byte, err error) {
		// A retry shares the original time budget instead of adding another
		// full timeout while images occupy the browser's connections.
		ctx, cancel := context.WithTimeout(ctx, artworkRequestTimeout)
		defer cancel()
		defer func() {
			if err != nil && !errors.Is(err, context.Canceled) {
				// fetchArtwork returns only host-free errors, never an upstream
				// URL, response body, or credential-bearing transport error.
				log.Printf("music artwork: %v", err)
			}
		}()
		for attempt := 0; attempt < 2; attempt++ {
			body, err = s.fetchArtwork(ctx, id)
			if err == nil {
				return body, nil
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			retry, delay := transporterr.Retry(err)
			if attempt == 1 || !retry || errors.Is(err, context.Canceled) {
				break
			}
			if delay < time.Second {
				delay = time.Second
			}
			if deadline, _ := ctx.Deadline(); time.Until(deadline) <= delay {
				break
			}
			if err = pause(ctx, delay); err != nil {
				return nil, err
			}
		}
		return nil, err
	})
}

func (s *Service) fetchArtwork(ctx context.Context, id string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://coverartarchive.org/release-group/"+id+"/front-500", nil)
	if err != nil {
		return nil, errUnavailable
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.art.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(err, errArtworkRedirect) {
			return nil, errArtworkRedirect
		}
		return nil, transporterr.Connection("music provider is unavailable: ", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return []byte{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, transporterr.HTTP(fmt.Sprintf("music provider returned HTTP %d", resp.StatusCode), resp)
	}
	if readErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(readErr, context.Canceled) {
			return nil, context.Canceled
		}
		return nil, transporterr.Connection("could not read music artwork: ", readErr)
	}
	if len(data) > 2<<20 {
		return nil, errors.New("music artwork exceeds the size limit")
	}
	switch http.DetectContentType(data) {
	case "image/jpeg", "image/png", "image/webp", "image/gif":
		return data, nil
	default:
		return nil, errors.New("invalid artwork response")
	}
}
